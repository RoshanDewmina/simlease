package main

import (
	"fmt"
	"io"
)

// skipIfPresentXcodebuildFlags are lease isolation flags kept when the user
// already supplied them on the xcodebuild argv.
var skipIfPresentXcodebuildFlags = map[string]bool{
	"-derivedDataPath":             true,
	"-clonedSourcePackagesDirPath": true,
	"-resultBundlePath":            true,
}

// MergeXcodebuildFlagsResult captures merge side effects for runExec.
type MergeXcodebuildFlagsResult struct {
	// SkipWarmDerivedData is true when the user supplied -derivedDataPath, so
	// warm-DD seed/publish must not touch the lease DerivedData path.
	SkipWarmDerivedData bool
	// DestinationReplaced is true when a user -destination was dropped in favor
	// of the lease-scoped destination.
	DestinationReplaced bool
}

// MergeXcodebuildIsolationFlags merges lease isolation flags into a user
// xcodebuild argv. Skip-if-present flags keep the caller's value; -destination
// is always replaced with the lease value when isolation supplies one.
func MergeXcodebuildIsolationFlags(userArgv, isolationFlags []string, warn io.Writer) ([]string, MergeXcodebuildFlagsResult) {
	if len(userArgv) == 0 {
		return userArgv, MergeXcodebuildFlagsResult{}
	}

	userFlags := indexXcodebuildValueFlags(userArgv[1:])
	var res MergeXcodebuildFlagsResult
	if _, ok := userFlags["-derivedDataPath"]; ok {
		res.SkipWarmDerivedData = true
	}

	userTail := userArgv[1:]
	if _, ok := userFlags["-destination"]; ok {
		userTail = stripXcodebuildFlag(userTail, "-destination")
		res.DestinationReplaced = true
		if warn != nil {
			fmt.Fprintln(warn, "simurgh exec: replacing caller -destination with lease-scoped simulator destination")
		}
	}

	inject := make([]string, 0, len(isolationFlags))
	for i := 0; i+1 < len(isolationFlags); i += 2 {
		flag := isolationFlags[i]
		val := isolationFlags[i+1]
		switch {
		case flag == "-destination":
			inject = append(inject, flag, val)
		case skipIfPresentXcodebuildFlags[flag]:
			if _, present := userFlags[flag]; present {
				continue
			}
			inject = append(inject, flag, val)
		default:
			inject = append(inject, flag, val)
		}
	}

	merged := append([]string{userArgv[0]}, append(inject, userTail...)...)
	return merged, res
}

func indexXcodebuildValueFlags(argv []string) map[string]string {
	out := make(map[string]string)
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if !isXcodebuildValueFlag(arg) {
			continue
		}
		if i+1 < len(argv) && !stringsHasPrefixDash(argv[i+1]) {
			out[arg] = argv[i+1]
			i++
		}
	}
	return out
}

func stripXcodebuildFlag(argv []string, flag string) []string {
	out := make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		if argv[i] == flag {
			if i+1 < len(argv) && !stringsHasPrefixDash(argv[i+1]) {
				i++
			}
			continue
		}
		out = append(out, argv[i])
	}
	return out
}

func isXcodebuildValueFlag(arg string) bool {
	switch arg {
	case "-derivedDataPath", "-clonedSourcePackagesDirPath", "-resultBundlePath", "-destination":
		return true
	default:
		return false
	}
}

func stringsHasPrefixDash(s string) bool {
	return len(s) > 0 && s[0] == '-'
}
