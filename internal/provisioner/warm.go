package provisioner

import (
	"context"
	"fmt"
	"strings"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/safety"
)

func warmTemplateName(model, runtimeVersion string) string {
	return fmt.Sprintf("simurgh-warm[%s:%s]", model, runtimeVersion)
}

// CreateWarmTemplate creates (or returns) a primed, shut-down warm template
// named simurgh-warm[<model>:<runtime>] for the given spec. Idempotent: if one
// already exists for the resolved (model, runtime), it is returned as-is.
//
// Lifecycle: create → boot → wait Booted → shutdown. The returned Device has
// Booted=false. Removal is not in this package — callers use
// safety.DeleteSimulatorClone.
func (p *Provisioner) CreateWarmTemplate(ctx context.Context, spec broker.DeviceSpec) (broker.Device, error) {
	if p == nil || p.client == nil {
		return broker.Device{}, fmt.Errorf("provisioner: client is required")
	}

	dt, err := p.resolveDeviceType(ctx, spec.Model)
	if err != nil {
		return broker.Device{}, err
	}
	rt, err := p.resolveRuntime(ctx, spec.Runtime)
	if err != nil {
		return broker.Device{}, err
	}

	warmName := warmTemplateName(spec.Model, rt.Version)

	mu := p.lockForBase(spec.Model, rt.Identifier)
	mu.Lock()
	defer mu.Unlock()

	devices, err := p.client.ListDevices(ctx)
	if err != nil {
		return broker.Device{}, cloneFailed(err)
	}
	for _, d := range devices {
		if d.Name == warmName && d.RuntimeIdentifier == rt.Identifier {
			return broker.Device{
				UDID:    d.UDID,
				Name:    d.Name,
				Runtime: rt.Version,
				Booted:  false,
			}, nil
		}
	}

	udid, err := p.client.Create(ctx, warmName, dt.Identifier, rt.Identifier)
	if err != nil {
		return broker.Device{}, cloneFailed(err)
	}

	if err := p.client.Boot(ctx, udid); err != nil {
		return broker.Device{}, cloneFailed(fmt.Errorf("boot: %w", err))
	}

	bootCtx, cancel := context.WithTimeout(ctx, bootWaitTimeout)
	defer cancel()
	if err := p.client.WaitBooted(bootCtx, udid); err != nil {
		return broker.Device{}, bootTimeout(udid)
	}

	if err := p.client.Shutdown(ctx, udid); err != nil {
		return broker.Device{}, cloneFailed(fmt.Errorf("shutdown: %w", err))
	}

	return broker.Device{
		UDID:    udid,
		Name:    warmName,
		Runtime: rt.Version,
		Booted:  false,
	}, nil
}

// ListWarmTemplates returns live devices whose names match
// safety.IsWarmTemplateName.
func (p *Provisioner) ListWarmTemplates(ctx context.Context) ([]broker.Device, error) {
	if p == nil || p.client == nil {
		return nil, fmt.Errorf("provisioner: client is required")
	}
	devices, err := p.client.ListDevices(ctx)
	if err != nil {
		return nil, cloneFailed(err)
	}
	var out []broker.Device
	for _, d := range devices {
		if !safety.IsWarmTemplateName(d.Name) {
			continue
		}
		out = append(out, broker.Device{
			UDID:    d.UDID,
			Name:    d.Name,
			Runtime: runtimeVersionFromWarmName(d.Name),
			Booted:  d.State == "Booted",
		})
	}
	return out, nil
}

// runtimeVersionFromWarmName extracts the runtime version from
// simurgh-warm[<model>:<runtime>] (last colon-separated segment inside brackets).
func runtimeVersionFromWarmName(name string) string {
	start := strings.Index(name, "[")
	end := strings.LastIndex(name, "]")
	if start < 0 || end <= start {
		return ""
	}
	inner := name[start+1 : end]
	i := strings.LastIndex(inner, ":")
	if i < 0 {
		return ""
	}
	return inner[i+1:]
}
