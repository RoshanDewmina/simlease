package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/ipc"
	"github.com/RoshanDewmina/simurgh/internal/provisioner"
	"github.com/RoshanDewmina/simurgh/internal/safety"
)

const cliAgentName = "simurgh-cli"

var cliProcessSessionID = newRandomSessionID("cli")

// rpcError wraps an IPC error triple so dialAndCall failures are classifiable.
type rpcError struct {
	e *ipc.Error
}

func (r *rpcError) Error() string {
	if r == nil || r.e == nil {
		return "rpc error"
	}
	return fmt.Sprintf("%s: %s", r.e.Code, r.e.Message)
}

// exitCodeForIPCError maps dial/RPC failures to ARCHITECTURE §9 exit codes:
// 0 ok, 2 queued/blocked, 3 invalid input, 4 environment, 5 internal.
func exitCodeForIPCError(err error) int {
	if err == nil {
		return 0
	}
	var dn *ipc.DaemonNotRunningError
	if errors.As(err, &dn) {
		return 4
	}
	var re *rpcError
	if errors.As(err, &re) && re.e != nil {
		switch re.e.Code {
		case broker.CodeCapacityQueued:
			return 2
		case broker.CodeLeaseNotFound, broker.CodeInvalidState,
			broker.CodeAcquireCancelled,
			provisioner.CodeDeviceTypeNotFound, provisioner.CodeRuntimeNotFound,
			safety.CodeDeviceNotManaged, safety.CodeDeviceNotFound:
			return 3
		case broker.CodeProvisionFailed, ipc.CodeDaemonNotRunning,
			provisioner.CodeCloneFailed, provisioner.CodeBootTimeout,
			safety.CodeDeviceDeleteFailed:
			return 4
		default:
			return 5
		}
	}
	return 5
}

func dialAndCall(ctx context.Context, method string, params any) (json.RawMessage, error) {
	socketPath, err := DefaultSocketPath()
	if err != nil {
		return nil, err
	}
	logPath, err := DefaultLogPath()
	if err != nil {
		return nil, err
	}
	conn, err := ipc.Dial(ctx, socketPath, logPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var rawParams json.RawMessage
	if params != nil {
		rawParams, err = json.Marshal(params)
		if err != nil {
			return nil, err
		}
	}
	req := ipc.Request{
		V:      ipc.ProtocolVersion,
		ID:     fmt.Sprintf("cli-%d", time.Now().UnixNano()),
		Method: method,
		Params: rawParams,
	}
	if err := ipc.Encode(conn, req); err != nil {
		return nil, err
	}
	resp, err := ipc.NewDecoder(conn).DecodeResponse()
	if err != nil {
		return nil, err
	}
	if resp.ID != req.ID {
		return nil, fmt.Errorf("response id mismatch: got %q want %q", resp.ID, req.ID)
	}
	if !resp.OK {
		if resp.Error != nil {
			return nil, &rpcError{e: resp.Error}
		}
		return nil, fmt.Errorf("%s returned ok=false", method)
	}
	return resp.Result, nil
}

func printIPCError(err error, asJSON bool) {
	var re *rpcError
	if errors.As(err, &re) && re.e != nil {
		if asJSON {
			_ = json.NewEncoder(os.Stdout).Encode(re.e)
			return
		}
		fmt.Fprintf(os.Stdout, "%s: %s\n", re.e.Code, re.e.Message)
		if re.e.QueuePosition != nil {
			fmt.Fprintf(os.Stdout, "queuePosition: %d\n", *re.e.QueuePosition)
		}
		if re.e.Reason != "" {
			fmt.Fprintf(os.Stdout, "reason: %s\n", re.e.Reason)
		}
		if re.e.ResourceClass != "" {
			fmt.Fprintf(os.Stdout, "resourceClass: %s\n", re.e.ResourceClass)
		}
		fmt.Fprintf(os.Stdout, "fix: %s\n", re.e.Fix)
		return
	}
	var dn *ipc.DaemonNotRunningError
	if errors.As(err, &dn) && dn.ErrorEnvelope != nil {
		if asJSON {
			_ = json.NewEncoder(os.Stdout).Encode(dn.ErrorEnvelope)
			return
		}
		fmt.Fprintf(os.Stdout, "%s: %s\n", dn.ErrorEnvelope.Code, dn.ErrorEnvelope.Message)
		fmt.Fprintf(os.Stdout, "fix: %s\n", dn.ErrorEnvelope.Fix)
		return
	}
	fmt.Fprintf(os.Stderr, "simurgh: %v\n", err)
}

func printErrorTriple(code, message, fix string, asJSON bool) {
	if asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{
			"code": code, "message": message, "fix": fix,
		})
		return
	}
	fmt.Fprintf(os.Stdout, "%s: %s\nfix: %s\n", code, message, fix)
}

func parseKV(s string) (string, string, error) {
	k, v, ok := strings.Cut(s, "=")
	if !ok || k == "" {
		return "", "", fmt.Errorf("expected k=v, got %q", s)
	}
	return k, v, nil
}

func parseLabelSelector(s string) (map[string]string, error) {
	out := make(map[string]string)
	if s == "" {
		return out, nil
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, err := parseKV(part)
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty label selector")
	}
	return out, nil
}

type acquireFlags struct {
	Model         string
	Runtime       string
	Labels        map[string]string
	TTL           time.Duration
	ReuseIfExists bool
	Wait          bool
	RM            bool
	Inactivity    time.Duration
	Hard          time.Duration
	JSON          bool
}

func parseAcquireFlags(args []string) (acquireFlags, error) {
	f := acquireFlags{Labels: make(map[string]string)}
	for i := 0; i < len(args); i++ {
		a := args[i]
		need := func(name string) (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", name)
			}
			i++
			return args[i], nil
		}
		switch a {
		case "--model":
			v, err := need(a)
			if err != nil {
				return f, err
			}
			f.Model = v
		case "--runtime":
			v, err := need(a)
			if err != nil {
				return f, err
			}
			f.Runtime = v
		case "--label":
			v, err := need(a)
			if err != nil {
				return f, err
			}
			k, val, err := parseKV(v)
			if err != nil {
				return f, err
			}
			f.Labels[k] = val
		case "--ttl":
			v, err := need(a)
			if err != nil {
				return f, err
			}
			d, err := time.ParseDuration(v)
			if err != nil {
				return f, fmt.Errorf("ttl: %w", err)
			}
			f.TTL = d
		case "--inactivity-timeout":
			v, err := need(a)
			if err != nil {
				return f, err
			}
			d, err := time.ParseDuration(v)
			if err != nil {
				return f, fmt.Errorf("inactivity-timeout: %w", err)
			}
			f.Inactivity = d
		case "--hard-timeout":
			v, err := need(a)
			if err != nil {
				return f, err
			}
			d, err := time.ParseDuration(v)
			if err != nil {
				return f, fmt.Errorf("hard-timeout: %w", err)
			}
			f.Hard = d
		case "--reuse-if-exists":
			// ReuseIfExists matches ACTIVE or PROVISIONING leases with the same
			// Spec, exact Labels, and non-empty Owner.Agent+SessionID (PID
			// excluded). ACTIVE renews; PROVISIONING attaches to the same grant.
			// Encode project/Xcode/scheme trust boundaries in Labels — they
			// are not first-class AcquireRequest fields.
			f.ReuseIfExists = true
		case "--wait":
			f.Wait = true
		case "--rm":
			f.RM = true
		case "--json":
			f.JSON = true
		case "-h", "--help", "help":
			return f, errors.New("help")
		default:
			return f, fmt.Errorf("unknown flag %q", a)
		}
	}
	if f.Model == "" {
		return f, fmt.Errorf("--model is required")
	}
	return f, nil
}

func durationString(d time.Duration) string {
	if d == 0 {
		return ""
	}
	return d.String()
}

func writeLastLease(id string) error {
	path, err := DefaultLastLeasePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(id+"\n"), 0o600)
}

func readLastLease() (string, error) {
	path, err := DefaultLastLeasePath()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(raw))
	if id == "" {
		return "", fmt.Errorf("last-lease file is empty")
	}
	return id, nil
}

func resolveLeaseIDOrLast(positional string) (string, error) {
	if positional != "" {
		return positional, nil
	}
	id, err := readLastLease()
	if err != nil {
		return "", &missingLeaseIDError{cause: err}
	}
	return id, nil
}

type missingLeaseIDError struct {
	cause error
}

func (e *missingLeaseIDError) Error() string {
	return "no lease-id given and no last-lease pointer found"
}

func (e *missingLeaseIDError) Unwrap() error { return e.cause }

func printLeaseHuman(lease broker.Lease) {
	udid := ""
	if lease.Device != nil {
		udid = lease.Device.UDID
	}
	fmt.Printf("id=%s state=%s model=%s", lease.ID, lease.State, lease.Spec.Model)
	if udid != "" {
		fmt.Printf(" udid=%s", udid)
	}
	fmt.Println()
}

func printLeaseResult(raw json.RawMessage, asJSON bool) error {
	if asJSON {
		var pretty any
		if err := json.Unmarshal(raw, &pretty); err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(pretty)
	}
	var wrap struct {
		Lease  broker.Lease `json:"lease"`
		Reused bool         `json:"reused"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return err
	}
	printLeaseHuman(wrap.Lease)
	fmt.Printf("reused: %t\n", wrap.Reused)
	return nil
}

func ownerPID(rm bool) int {
	if rm {
		return os.Getppid()
	}
	return os.Getpid()
}

func cliSessionID() string {
	if explicit := strings.TrimSpace(os.Getenv("SIMURGH_SESSION_ID")); explicit != "" {
		return explicit
	}
	// Random and stable only for this CLI process. Cross-invocation reuse
	// requires an explicit SIMURGH_SESSION_ID.
	return cliProcessSessionID
}

func newRandomSessionID(prefix string) string {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		panic(fmt.Sprintf("simurgh: generate session identity: %v", err))
	}
	return prefix + ":" + hex.EncodeToString(entropy[:])
}

func runAcquire(args []string) int {
	f, err := parseAcquireFlags(args)
	if err != nil {
		if err.Error() == "help" {
			fmt.Println(`usage: simurgh acquire --model NAME [--runtime VER] [--label k=v]...
        [--ttl DUR] [--reuse-if-exists] [--wait] [--rm]
        [--inactivity-timeout DUR] [--hard-timeout DUR] [--json]
reuse requires --label project=NAME; set SIMURGH_SESSION_ID for reuse across CLI invocations`)
			return 0
		}
		printErrorTriple("INVALID_INPUT", err.Error(), "simurgh acquire --model NAME ...", f.JSON)
		return 3
	}

	params := map[string]any{
		"spec": map[string]string{
			"platform": "iOS",
			"model":    f.Model,
			"runtime":  f.Runtime,
		},
		"labels":           f.Labels,
		"ttl":              durationString(f.TTL),
		"reuseIfExists":    f.ReuseIfExists,
		"wait":             f.Wait,
		"releaseOnPIDExit": f.RM,
		"owner": map[string]any{
			// Agent is a fixed CLI identity; real calling-agent detection is
			// deferred to simurgh init / env-based identification (M2/M3).
			// With --rm the lease must die with the *session* that wants it,
			// not with this one-shot acquire process (which exits right after
			// printing the grant — binding to os.Getpid() would have the
			// watcher reclaim every --rm lease within one tick). The parent
			// is the invoking shell/agent process, the thing whose death
			// actually means "session over".
			"pid":       ownerPID(f.RM),
			"agent":     cliAgentName,
			"sessionId": cliSessionID(),
		},
		"timeouts": map[string]string{
			"inactivity": durationString(f.Inactivity),
			"hard":       durationString(f.Hard),
		},
	}

	raw, err := dialAndCall(context.Background(), "lease.acquire", params)
	if err != nil {
		printIPCError(err, f.JSON)
		return exitCodeForIPCError(err)
	}

	var wrap struct {
		Lease  broker.Lease `json:"lease"`
		Reused bool         `json:"reused"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh acquire: decode: %v\n", err)
		return 5
	}
	if err := writeLastLease(wrap.Lease.ID); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh acquire: write last-lease: %v\n", err)
		return 5
	}
	if err := printLeaseResult(raw, f.JSON); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh acquire: %v\n", err)
		return 5
	}
	return 0
}

func runRelease(args []string) int {
	var (
		leaseID string
		all     bool
		labels  = make(map[string]string)
		asJSON  bool
		posSeen bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--all":
			all = true
		case a == "--json":
			asJSON = true
		case a == "--label":
			if i+1 >= len(args) {
				printErrorTriple("INVALID_INPUT", "--label requires k=v", "simurgh release [--all --label k=v] [lease-id]", asJSON)
				return 3
			}
			i++
			k, v, err := parseKV(args[i])
			if err != nil {
				printErrorTriple("INVALID_INPUT", err.Error(), "simurgh release --label k=v", asJSON)
				return 3
			}
			labels[k] = v
		case a == "-h" || a == "--help" || a == "help":
			fmt.Println("usage: simurgh release [<lease-id>] [--all --label k=v] [--json]")
			return 0
		case strings.HasPrefix(a, "-"):
			printErrorTriple("INVALID_INPUT", fmt.Sprintf("unknown flag %q", a), "simurgh release [<lease-id>] [--all --label k=v]", asJSON)
			return 3
		default:
			if posSeen {
				printErrorTriple("INVALID_INPUT", "only one lease-id allowed", "simurgh release [<lease-id>]", asJSON)
				return 3
			}
			leaseID = a
			posSeen = true
		}
	}

	if all {
		if len(labels) == 0 {
			printErrorTriple("INVALID_INPUT", "--all requires at least one --label k=v", "simurgh release --all --label k=v", asJSON)
			return 3
		}
		return releaseAllByLabels(labels, asJSON)
	}

	id, err := resolveLeaseIDOrLast(leaseID)
	if err != nil {
		var miss *missingLeaseIDError
		if errors.As(err, &miss) {
			printErrorTriple(
				"INVALID_INPUT",
				"no lease-id given and no last-lease pointer found",
				"pass a lease-id or run `simurgh acquire` first",
				asJSON,
			)
			return 3
		}
		printErrorTriple("INVALID_INPUT", err.Error(), "simurgh release [<lease-id>]", asJSON)
		return 3
	}

	raw, err := dialAndCall(context.Background(), "lease.release", map[string]any{"id": id})
	if err != nil {
		printIPCError(err, asJSON)
		return exitCodeForIPCError(err)
	}
	if err := printLeaseResult(raw, asJSON); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh release: %v\n", err)
		return 5
	}
	return 0
}

func releaseAllByLabels(labels map[string]string, asJSON bool) int {
	raw, err := dialAndCall(context.Background(), "lease.list", map[string]any{"labels": labels})
	if err != nil {
		printIPCError(err, asJSON)
		return exitCodeForIPCError(err)
	}
	var wrap struct {
		Leases []broker.Lease `json:"leases"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh release: %v\n", err)
		return 5
	}
	okN, failN := 0, 0
	var failures []string
	for _, l := range wrap.Leases {
		_, err := dialAndCall(context.Background(), "lease.release", map[string]any{"id": l.ID})
		if err != nil {
			failN++
			failures = append(failures, fmt.Sprintf("%s: %v", l.ID, err))
			continue
		}
		okN++
	}
	summary := map[string]any{
		"matched":  len(wrap.Leases),
		"released": okN,
		"failed":   failN,
		"failures": failures,
	}
	if asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(summary)
	} else {
		fmt.Printf("matched=%d released=%d failed=%d\n", len(wrap.Leases), okN, failN)
		for _, f := range failures {
			fmt.Println(f)
		}
	}
	if failN > 0 {
		return 5
	}
	return 0
}

func runRenew(args []string) int {
	var (
		leaseID string
		ttl     time.Duration
		asJSON  bool
		posSeen bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--ttl":
			if i+1 >= len(args) {
				printErrorTriple("INVALID_INPUT", "--ttl requires a duration", "simurgh renew <lease-id> [--ttl 30m]", asJSON)
				return 3
			}
			i++
			d, err := time.ParseDuration(args[i])
			if err != nil {
				printErrorTriple("INVALID_INPUT", fmt.Sprintf("ttl: %v", err), "simurgh renew <lease-id> [--ttl 30m]", asJSON)
				return 3
			}
			ttl = d
		case a == "--json":
			asJSON = true
		case a == "-h" || a == "--help" || a == "help":
			fmt.Println("usage: simurgh renew <lease-id> [--ttl 30m] [--json]")
			return 0
		case strings.HasPrefix(a, "-"):
			printErrorTriple("INVALID_INPUT", fmt.Sprintf("unknown flag %q", a), "simurgh renew <lease-id> [--ttl 30m]", asJSON)
			return 3
		default:
			if posSeen {
				printErrorTriple("INVALID_INPUT", "only one lease-id allowed", "simurgh renew <lease-id>", asJSON)
				return 3
			}
			leaseID = a
			posSeen = true
		}
	}
	// ARCHITECTURE §9 documents renew <lease-id> as required (no brackets);
	// do not fall back to last-lease.
	if leaseID == "" {
		printErrorTriple("INVALID_INPUT", "lease-id is required", "simurgh renew <lease-id> [--ttl 30m]", asJSON)
		return 3
	}

	params := map[string]any{"id": leaseID}
	if ttl > 0 {
		params["ttl"] = ttl.String()
	}
	raw, err := dialAndCall(context.Background(), "lease.renew", params)
	if err != nil {
		printIPCError(err, asJSON)
		return exitCodeForIPCError(err)
	}
	if err := printLeaseResult(raw, asJSON); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh renew: %v\n", err)
		return 5
	}
	return 0
}

func runStatus(args []string) int {
	var showQueue, asJSON bool
	for _, a := range args {
		switch a {
		case "--queue":
			showQueue = true
		case "--json":
			asJSON = true
		case "-h", "--help", "help":
			fmt.Println("usage: simurgh status [--queue] [--json]")
			return 0
		default:
			printErrorTriple("INVALID_INPUT", fmt.Sprintf("unknown flag %q", a), "simurgh status [--queue] [--json]", asJSON)
			return 3
		}
	}

	raw, err := dialAndCall(context.Background(), "pool.status", map[string]any{})
	if err != nil {
		printIPCError(err, asJSON)
		// Status is a query; pool dial failure is still an environment/internal
		// problem, but the command itself is never "queued/blocked".
		code := exitCodeForIPCError(err)
		if code == 2 {
			return 5
		}
		return code
	}

	var status any
	if err := json.Unmarshal(raw, &status); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh status: %v\n", err)
		return 5
	}

	var queueOut []map[string]any
	if showQueue {
		// Exact per-lease queue position is not exposed by any RPC beyond
		// CAPACITY_QUEUED at acquire-time. Approximate by listing QUEUED
		// leases sorted by AcquiredAt ascending and assign 1-based ordinals
		// in that order (best-effort FIFO approximation across all specs).
		listRaw, err := dialAndCall(context.Background(), "lease.list", map[string]any{})
		if err != nil {
			printIPCError(err, asJSON)
			return exitCodeForIPCError(err)
		}
		var wrap struct {
			Leases []broker.Lease `json:"leases"`
		}
		if err := json.Unmarshal(listRaw, &wrap); err != nil {
			fmt.Fprintf(os.Stderr, "simurgh status: %v\n", err)
			return 5
		}
		var queued []broker.Lease
		for _, l := range wrap.Leases {
			if l.State == broker.LeaseQueued {
				queued = append(queued, l)
			}
		}
		sort.Slice(queued, func(i, j int) bool {
			return queued[i].AcquiredAt.Before(queued[j].AcquiredAt)
		})
		for i, l := range queued {
			queueOut = append(queueOut, map[string]any{
				"approximatePosition": i + 1,
				"id":                  l.ID,
				"model":               l.Spec.Model,
				"acquiredAt":          l.AcquiredAt,
			})
		}
	}

	if asJSON {
		out := map[string]any{"pool": status}
		if showQueue {
			out["queue"] = queueOut
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return 0
	}

	b, _ := json.MarshalIndent(status, "", "  ")
	fmt.Println(string(b))
	if showQueue {
		if len(queueOut) == 0 {
			fmt.Println("queue: (empty)")
		} else {
			fmt.Println("queue (approximate position by AcquiredAt order):")
			for _, q := range queueOut {
				fmt.Printf("  #%v %s model=%s\n", q["approximatePosition"], q["id"], q["model"])
			}
		}
	}
	return 0
}

func runList(args []string) int {
	var (
		selector string
		asJSON   bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--label-selector":
			if i+1 >= len(args) {
				printErrorTriple("INVALID_INPUT", "--label-selector requires k=v,...", "simurgh list [--label-selector k=v,...]", asJSON)
				return 3
			}
			i++
			selector = args[i]
		case a == "--json":
			asJSON = true
		case a == "-h" || a == "--help" || a == "help":
			fmt.Println("usage: simurgh list [--label-selector k=v,...] [--json]")
			return 0
		default:
			printErrorTriple("INVALID_INPUT", fmt.Sprintf("unknown flag %q", a), "simurgh list [--label-selector k=v,...]", asJSON)
			return 3
		}
	}

	params := map[string]any{}
	if selector != "" {
		labels, err := parseLabelSelector(selector)
		if err != nil {
			printErrorTriple("INVALID_INPUT", err.Error(), "simurgh list [--label-selector k=v,...]", asJSON)
			return 3
		}
		params["labels"] = labels
	}

	raw, err := dialAndCall(context.Background(), "lease.list", params)
	if err != nil {
		printIPCError(err, asJSON)
		return exitCodeForIPCError(err)
	}
	var wrap struct {
		Leases []broker.Lease `json:"leases"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh list: %v\n", err)
		return 5
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(wrap)
		return 0
	}
	fmt.Printf("%-20s %-14s %-20s %s\n", "ID", "STATE", "MODEL", "UDID")
	for _, l := range wrap.Leases {
		udid := ""
		if l.Device != nil {
			udid = l.Device.UDID
		}
		fmt.Printf("%-20s %-14s %-20s %s\n", l.ID, l.State, l.Spec.Model, udid)
	}
	return 0
}
