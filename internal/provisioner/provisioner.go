package provisioner

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
)

// bootWaitTimeout bounds how long Provision waits for Booted after simctl boot.
// Cold boots on recent Xcode/iOS runtimes routinely exceed 60s on this Mac.
const bootWaitTimeout = 120 * time.Second

// Provisioner implements broker.Provisioner via SimctlClient.
type Provisioner struct {
	client SimctlClient

	// baseLocks serializes ensureBaseDevice's list-then-create per
	// (model, runtime identifier) so concurrent same-spec Provisions share one
	// base. Unrelated keys are not serialized against each other.
	baseMu    sync.Mutex
	baseLocks map[string]*sync.Mutex
}

// New returns a Provisioner that uses client for all simctl operations.
func New(client SimctlClient) *Provisioner {
	return &Provisioner{
		client:    client,
		baseLocks: make(map[string]*sync.Mutex),
	}
}

// Provision clones a Simurgh base device for the lease, boots it, and waits
// for Booted. It never deletes, erases, or shuts down any device.
func (p *Provisioner) Provision(ctx context.Context, req broker.ProvisionRequest) (broker.Device, error) {
	if p == nil || p.client == nil {
		return broker.Device{}, fmt.Errorf("provisioner: client is required")
	}
	if req.LeaseID == "" {
		return broker.Device{}, fmt.Errorf("provisioner: LeaseID is required")
	}

	dt, err := p.resolveDeviceType(ctx, req.Spec.Model)
	if err != nil {
		return broker.Device{}, err
	}

	rt, err := p.resolveRuntime(ctx, req.Spec.Runtime)
	if err != nil {
		return broker.Device{}, err
	}

	baseUDID, err := p.ensureBaseDevice(ctx, req.Spec.Model, dt.Identifier, rt)
	if err != nil {
		return broker.Device{}, err
	}

	cloneName := cloneDeviceName(req.LeaseID, req.Spec.Model, rt.Version)
	tempName := fmt.Sprintf("simurgh-provisioning-%s", req.LeaseID)

	cloneUDID, err := p.client.Clone(ctx, baseUDID, tempName)
	if err != nil {
		return broker.Device{}, cloneFailed(err)
	}

	if err := p.client.Rename(ctx, cloneUDID, cloneName); err != nil {
		return broker.Device{}, cloneFailed(err)
	}

	if err := p.client.Boot(ctx, cloneUDID); err != nil {
		return broker.Device{}, cloneFailed(fmt.Errorf("boot: %w", err))
	}

	bootCtx, cancel := context.WithTimeout(ctx, bootWaitTimeout)
	defer cancel()
	if err := p.client.WaitBooted(bootCtx, cloneUDID); err != nil {
		return broker.Device{}, bootTimeout(cloneUDID)
	}

	return broker.Device{
		UDID:    cloneUDID,
		Name:    cloneName,
		Runtime: rt.Version,
		CloneOf: baseUDID,
		Booted:  true,
	}, nil
}

func (p *Provisioner) resolveDeviceType(ctx context.Context, model string) (DeviceType, error) {
	types, err := p.client.ListDeviceTypes(ctx)
	if err != nil {
		return DeviceType{}, deviceTypeNotFound(model)
	}
	for _, dt := range types {
		if dt.Name == model {
			return dt, nil
		}
	}
	return DeviceType{}, deviceTypeNotFound(model)
}

// resolveRuntime picks an installed iOS runtime.
//
// When want is empty, the newest available iOS runtime is selected by comparing
// Version strings with a numeric dotted-segment order (not simctl list order).
// When want is set, exact match on version/name/identifier wins; otherwise a
// prefix match on those fields, preferring the newest among matches.
func (p *Provisioner) resolveRuntime(ctx context.Context, want string) (Runtime, error) {
	runtimes, err := p.client.ListRuntimes(ctx)
	if err != nil {
		return Runtime{}, runtimeNotFound(want)
	}

	var ios []Runtime
	for _, rt := range runtimes {
		if !rt.IsAvailable {
			continue
		}
		if rt.PlatformName != "iOS" && !strings.HasPrefix(rt.Name, "iOS") {
			continue
		}
		ios = append(ios, rt)
	}
	if len(ios) == 0 {
		return Runtime{}, runtimeNotFound(want)
	}

	if want == "" {
		return newestRuntime(ios), nil
	}

	var exact, prefix []Runtime
	for _, rt := range ios {
		switch {
		case rt.Version == want || rt.Name == want || rt.Identifier == want:
			exact = append(exact, rt)
		case strings.HasPrefix(rt.Version, want) ||
			strings.HasPrefix(rt.Name, want) ||
			strings.HasPrefix(rt.Identifier, want):
			prefix = append(prefix, rt)
		}
	}
	if len(exact) > 0 {
		return newestRuntime(exact), nil
	}
	if len(prefix) > 0 {
		return newestRuntime(prefix), nil
	}
	return Runtime{}, runtimeNotFound(want)
}

func (p *Provisioner) ensureBaseDevice(ctx context.Context, model, deviceTypeID string, rt Runtime) (string, error) {
	// Safety-invariant adjacent: base devices are named simurgh-base[...], which
	// deliberately does NOT match ^simurgh-(clone|warm)\[.*]$. The future reaper
	// must never treat bases as deletable lease clones.
	//
	// When a warm template simurgh-warm[<model>:<runtime>] exists for this key,
	// prefer it as the clone source (ARCHITECTURE §6). Still under lockForBase
	// so concurrent Provision/CreateWarmTemplate for the same key cannot race.
	baseName := baseDeviceName(model, rt.Version)
	warmName := warmTemplateName(model, rt.Version)

	mu := p.lockForBase(model, rt.Identifier)
	mu.Lock()
	defer mu.Unlock()

	devices, err := p.client.ListDevices(ctx)
	if err != nil {
		return "", cloneFailed(err)
	}
	for _, d := range devices {
		if d.Name == warmName && d.RuntimeIdentifier == rt.Identifier {
			return d.UDID, nil
		}
	}
	for _, d := range devices {
		if d.Name == baseName && d.RuntimeIdentifier == rt.Identifier {
			return d.UDID, nil
		}
	}

	udid, err := p.client.Create(ctx, baseName, deviceTypeID, rt.Identifier)
	if err != nil {
		return "", cloneFailed(err)
	}
	return udid, nil
}

func (p *Provisioner) lockForBase(model, runtimeID string) *sync.Mutex {
	key := model + "\x00" + runtimeID
	p.baseMu.Lock()
	defer p.baseMu.Unlock()
	mu, ok := p.baseLocks[key]
	if !ok {
		mu = &sync.Mutex{}
		p.baseLocks[key] = mu
	}
	return mu
}

func baseDeviceName(model, runtimeVersion string) string {
	return fmt.Sprintf("simurgh-base[%s:%s]", model, runtimeVersion)
}

func cloneDeviceName(leaseID, model, runtimeVersion string) string {
	return fmt.Sprintf("simurgh-clone[%s:%s:%s]", leaseID, model, runtimeVersion)
}

func newestRuntime(runtimes []Runtime) Runtime {
	best := runtimes[0]
	for _, rt := range runtimes[1:] {
		if compareVersion(rt.Version, best.Version) > 0 {
			best = rt
		}
	}
	return best
}

// compareVersion compares dotted numeric version strings (e.g. "27.0" vs "26.1").
// Returns >0 if a>b, <0 if a<b, 0 if equal or both empty/unparseable equally.
func compareVersion(a, b string) int {
	as := versionParts(a)
	bs := versionParts(b)
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var av, bv int
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		if av != bv {
			return av - bv
		}
	}
	return 0
}

func versionParts(v string) []int {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		// Strip non-numeric suffix (e.g. "27.0-beta" → 27, 0).
		num := p
		for i, r := range p {
			if r < '0' || r > '9' {
				num = p[:i]
				break
			}
		}
		if num == "" {
			out = append(out, 0)
			continue
		}
		n, err := strconv.Atoi(num)
		if err != nil {
			out = append(out, 0)
			continue
		}
		out = append(out, n)
	}
	return out
}
