package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestMergeXcodebuildIsolationFlags_NoUserFlagsInjectsAll(t *testing.T) {
	user := []string{"xcodebuild", "build", "-scheme", "App"}
	iso := []string{
		"-derivedDataPath", "/lease/DD",
		"-clonedSourcePackagesDirPath", "/lease/SwiftPM",
		"-destination", "platform=iOS Simulator,id=UDID-1",
	}

	got, res := MergeXcodebuildIsolationFlags(user, iso, nil)
	want := []string{
		"xcodebuild",
		"-derivedDataPath", "/lease/DD",
		"-clonedSourcePackagesDirPath", "/lease/SwiftPM",
		"-destination", "platform=iOS Simulator,id=UDID-1",
		"build", "-scheme", "App",
	}
	assertArgvEqual(t, got, want)
	if res.SkipWarmDerivedData || res.DestinationReplaced {
		t.Fatalf("result = %+v, want no skips/replacements", res)
	}
}

func TestMergeXcodebuildIsolationFlags_SkipUserDerivedDataSwiftPMResultBundle(t *testing.T) {
	user := []string{
		"xcodebuild", "test-without-building",
		"-derivedDataPath", "/user/DD",
		"-clonedSourcePackagesDirPath", "/user/SwiftPM",
		"-resultBundlePath", "/user/run.xcresult",
	}
	iso := []string{
		"-derivedDataPath", "/lease/DD",
		"-clonedSourcePackagesDirPath", "/lease/SwiftPM",
		"-destination", "platform=iOS Simulator,id=UDID-1",
	}
	extra := []string{"-resultBundlePath", "/lease/run.xcresult"}

	got, res := MergeXcodebuildIsolationFlags(user, append(iso, extra...), nil)
	want := []string{
		"xcodebuild",
		"-destination", "platform=iOS Simulator,id=UDID-1",
		"test-without-building",
		"-derivedDataPath", "/user/DD",
		"-clonedSourcePackagesDirPath", "/user/SwiftPM",
		"-resultBundlePath", "/user/run.xcresult",
	}
	assertArgvEqual(t, got, want)
	if !res.SkipWarmDerivedData {
		t.Fatal("expected SkipWarmDerivedData")
	}
	if res.DestinationReplaced {
		t.Fatal("user did not pass -destination")
	}
}

func TestMergeXcodebuildIsolationFlags_ReplaceDestinationWithWarning(t *testing.T) {
	user := []string{
		"xcodebuild", "build",
		"-destination", "platform=iOS Simulator,name=iPhone 16",
	}
	iso := []string{
		"-derivedDataPath", "/lease/DD",
		"-clonedSourcePackagesDirPath", "/lease/SwiftPM",
		"-destination", "platform=iOS Simulator,id=UDID-LEASE",
	}

	var warn bytes.Buffer
	got, res := MergeXcodebuildIsolationFlags(user, iso, &warn)
	want := []string{
		"xcodebuild",
		"-derivedDataPath", "/lease/DD",
		"-clonedSourcePackagesDirPath", "/lease/SwiftPM",
		"-destination", "platform=iOS Simulator,id=UDID-LEASE",
		"build",
	}
	assertArgvEqual(t, got, want)
	if !res.DestinationReplaced {
		t.Fatal("expected DestinationReplaced")
	}
	if !strings.Contains(warn.String(), "replacing caller -destination") {
		t.Fatalf("warning = %q", warn.String())
	}
}

func TestMergeXcodebuildIsolationFlags_MixedUserFlags(t *testing.T) {
	user := []string{
		"xcodebuild", "test",
		"-derivedDataPath", "/user/DD",
		"-destination", "platform=iOS Simulator,name=Wrong",
		"-scheme", "App",
	}
	iso := []string{
		"-derivedDataPath", "/lease/DD",
		"-clonedSourcePackagesDirPath", "/lease/SwiftPM",
		"-destination", "platform=iOS Simulator,id=UDID-LEASE",
	}
	extra := []string{"-resultBundlePath", "/lease/run.xcresult"}

	got, res := MergeXcodebuildIsolationFlags(user, append(iso, extra...), nil)
	want := []string{
		"xcodebuild",
		"-clonedSourcePackagesDirPath", "/lease/SwiftPM",
		"-destination", "platform=iOS Simulator,id=UDID-LEASE",
		"-resultBundlePath", "/lease/run.xcresult",
		"test",
		"-derivedDataPath", "/user/DD",
		"-scheme", "App",
	}
	assertArgvEqual(t, got, want)
	if !res.SkipWarmDerivedData || !res.DestinationReplaced {
		t.Fatalf("result = %+v", res)
	}
}

func assertArgvEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("argv len = %d want %d\ngot  = %#v\nwant = %#v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("argv[%d] = %q want %q\ngot  = %#v\nwant = %#v", i, got[i], want[i], got, want)
		}
	}
}
