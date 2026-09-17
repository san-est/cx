package main

import (
	"runtime/debug"
	"strings"
	"testing"
)

func buildInfo(mainVersion string, settings map[string]string) *debug.BuildInfo {
	bi := &debug.BuildInfo{}
	bi.Main.Version = mainVersion
	for k, v := range settings {
		bi.Settings = append(bi.Settings, debug.BuildSetting{Key: k, Value: v})
	}
	return bi
}

func TestVersionStringPrefersTheStampedRelease(t *testing.T) {
	got := versionString("v0.1.0", buildInfo("(devel)", map[string]string{
		"vcs.revision": "0123456789abcdef0123456789abcdef01234567",
	}), true)

	if !strings.HasPrefix(got, "cx v0.1.0 (0123456789ab)") {
		t.Errorf("version = %q, want the stamped release and a short revision", got)
	}
}

func TestVersionStringFallsBackToTheModuleVersion(t *testing.T) {
	// This is what `go install github.com/san-est/cx/cmd/cx@v0.1.0` produces:
	// nothing stamped at link time, but a module version recorded by the
	// toolchain.
	got := versionString("", buildInfo("v0.1.0", nil), true)

	if !strings.HasPrefix(got, "cx v0.1.0") {
		t.Errorf("version = %q, want the module version", got)
	}
}

func TestVersionStringReportsADirtyTree(t *testing.T) {
	// A dirty build is not the commit it names, which is worth knowing before
	// chasing a bug against that source.
	got := versionString("", buildInfo("(devel)", map[string]string{
		"vcs.revision": "abcdef1234567890",
		"vcs.modified": "true",
	}), true)

	if !strings.Contains(got, "(devel)") {
		t.Errorf("version = %q, want it marked as a development build", got)
	}
	if !strings.Contains(got, "dirty") {
		t.Errorf("version = %q, want the dirty tree reported", got)
	}
}

func TestVersionStringSurvivesMissingBuildInfo(t *testing.T) {
	// debug.ReadBuildInfo reports ok=false for a binary built without module
	// support. Reading the nil BuildInfo anyway would panic on `cx version`.
	got := versionString("", nil, false)

	if !strings.HasPrefix(got, "cx (devel)") {
		t.Errorf("version = %q, want a usable string with no build info", got)
	}
	if !strings.Contains(got, "/") {
		t.Errorf("version = %q, want the os/arch line", got)
	}
}

func TestVersionStringAlwaysNamesTheToolchainAndPlatform(t *testing.T) {
	got := versionString("v0.1.0", buildInfo("(devel)", nil), true)

	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 {
		t.Fatalf("version = %q, want exactly two lines", got)
	}
	if !strings.HasPrefix(lines[1], "go") {
		t.Errorf("second line = %q, want the Go toolchain and platform", lines[1])
	}
}
