package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// version is stamped at link time for release builds:
//
//	go build -ldflags "-X main.version=v0.1.0"
//
// It stays empty for `go install` and local builds. There the module version
// and VCS stamps the Go toolchain records itself are both more accurate and
// harder to get wrong than a constant someone has to remember to bump.
var version = ""

// versionString identifies a binary well enough to act on a bug report: which
// release it is, or failing that the commit it was built from and whether the
// working tree was dirty at the time.
//
// It takes the build info rather than reading it, so the assembly of the string
// is testable without building binaries at several versions.
func versionString(stamped string, bi *debug.BuildInfo, ok bool) string {
	var b strings.Builder
	b.WriteString("cx ")

	switch {
	case stamped != "":
		b.WriteString(stamped)
	case ok && bi.Main.Version != "" && bi.Main.Version != "(devel)":
		// `go install module@version` records the version here.
		b.WriteString(bi.Main.Version)
	default:
		b.WriteString("(devel)")
	}

	if ok {
		var revision, when string
		var dirty bool
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				revision = s.Value
			case "vcs.time":
				when = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		if revision != "" {
			if len(revision) > 12 {
				revision = revision[:12]
			}
			b.WriteString(" (" + revision)
			if dirty {
				// A dirty build is not the commit it claims to be, which is
				// worth knowing before chasing a bug against that source.
				b.WriteString(", dirty")
			}
			if when != "" {
				b.WriteString(", " + when)
			}
			b.WriteString(")")
		}
	}

	b.WriteString("\n")
	b.WriteString(runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH + "\n")
	return b.String()
}

func runVersion() int {
	bi, ok := debug.ReadBuildInfo()
	fmt.Print(versionString(version, bi, ok))
	return 0
}
