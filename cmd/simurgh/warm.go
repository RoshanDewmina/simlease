package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/RoshanDewmina/simurgh/internal/broker"
)

func runWarm(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: simurgh warm create|list|remove ...")
		return 2
	}
	switch args[0] {
	case "create":
		return runWarmCreate(args[1:])
	case "list":
		return runWarmList(args[1:])
	case "remove":
		return runWarmRemove(args[1:])
	case "-h", "--help", "help":
		fmt.Println(`usage: simurgh warm create --model NAME [--runtime VER] [--json]
       simurgh warm list [--json]
       simurgh warm remove --model NAME [--runtime VER] [--json]`)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "usage: simurgh warm create|list|remove ...\n")
		return 2
	}
}

type warmFlags struct {
	Model   string
	Runtime string
	JSON    bool
}

func parseWarmFlags(args []string, requireModel bool) (warmFlags, error) {
	var f warmFlags
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
		case "--json":
			f.JSON = true
		case "-h", "--help", "help":
			return f, errors.New("help")
		default:
			return f, fmt.Errorf("unknown flag %q", a)
		}
	}
	if requireModel && f.Model == "" {
		return f, fmt.Errorf("--model is required")
	}
	return f, nil
}

func runWarmCreate(args []string) int {
	f, err := parseWarmFlags(args, true)
	if err != nil {
		if err.Error() == "help" {
			fmt.Println("usage: simurgh warm create --model NAME [--runtime VER] [--json]")
			return 0
		}
		printErrorTriple("INVALID_INPUT", err.Error(), "simurgh warm create --model NAME [--runtime VER]", f.JSON)
		return 3
	}
	params := map[string]any{
		"model":   f.Model,
		"runtime": f.Runtime,
	}
	raw, err := dialAndCall(context.Background(), "warm.create", params)
	if err != nil {
		printIPCError(err, f.JSON)
		return exitCodeForIPCError(err)
	}
	return printWarmDeviceResult(raw, f.JSON)
}

func runWarmList(args []string) int {
	f, err := parseWarmFlags(args, false)
	if err != nil {
		if err.Error() == "help" {
			fmt.Println("usage: simurgh warm list [--json]")
			return 0
		}
		printErrorTriple("INVALID_INPUT", err.Error(), "simurgh warm list [--json]", f.JSON)
		return 3
	}
	raw, err := dialAndCall(context.Background(), "warm.list", map[string]any{})
	if err != nil {
		printIPCError(err, f.JSON)
		return exitCodeForIPCError(err)
	}
	if f.JSON {
		var pretty any
		if err := json.Unmarshal(raw, &pretty); err != nil {
			fmt.Fprintf(os.Stderr, "simurgh warm list: %v\n", err)
			return 5
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(pretty)
		return 0
	}
	var wrap struct {
		Devices []broker.Device `json:"devices"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh warm list: %v\n", err)
		return 5
	}
	fmt.Printf("%-40s %-10s %s\n", "NAME", "RUNTIME", "UDID")
	for _, d := range wrap.Devices {
		fmt.Printf("%-40s %-10s %s\n", d.Name, d.Runtime, d.UDID)
	}
	return 0
}

func runWarmRemove(args []string) int {
	f, err := parseWarmFlags(args, true)
	if err != nil {
		if err.Error() == "help" {
			fmt.Println("usage: simurgh warm remove --model NAME [--runtime VER] [--json]")
			return 0
		}
		printErrorTriple("INVALID_INPUT", err.Error(), "simurgh warm remove --model NAME [--runtime VER]", f.JSON)
		return 3
	}
	params := map[string]any{
		"model":   f.Model,
		"runtime": f.Runtime,
	}
	raw, err := dialAndCall(context.Background(), "warm.remove", params)
	if err != nil {
		printIPCError(err, f.JSON)
		return exitCodeForIPCError(err)
	}
	return printWarmDeviceResult(raw, f.JSON)
}

func printWarmDeviceResult(raw json.RawMessage, asJSON bool) int {
	if asJSON {
		var pretty any
		if err := json.Unmarshal(raw, &pretty); err != nil {
			fmt.Fprintf(os.Stderr, "simurgh warm: %v\n", err)
			return 5
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(pretty)
		return 0
	}
	var wrap struct {
		Device broker.Device `json:"device"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh warm: %v\n", err)
		return 5
	}
	fmt.Printf("name=%s runtime=%s udid=%s booted=%v\n",
		wrap.Device.Name, wrap.Device.Runtime, wrap.Device.UDID, wrap.Device.Booted)
	return 0
}
