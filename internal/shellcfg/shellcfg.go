// Package shellcfg generates the shell code that cx hands back to its wrapper
// function.
//
// A program cannot change the environment of the shell that started it: when cx
// exits, anything it exported dies with it. The only way to affect the calling
// shell is to print shell code and have a wrapper function evaluate it. This
// package builds that code.
package shellcfg

import (
	"fmt"
	"sort"
	"strings"
)

// Script is a set of environment changes for the calling shell to apply.
type Script struct {
	exports map[string]string
	unsets  map[string]bool
	notes   []string
}

// New returns an empty Script.
func New() *Script {
	return &Script{exports: map[string]string{}, unsets: map[string]bool{}}
}

// Export records a variable to set in the calling shell.
func (s *Script) Export(name, value string) *Script {
	delete(s.unsets, name)
	s.exports[name] = value
	return s
}

// Unset records a variable to remove from the calling shell.
func (s *Script) Unset(name string) *Script {
	delete(s.exports, name)
	s.unsets[name] = true
	return s
}

// Note records a message to print to the user when the script is applied.
func (s *Script) Note(format string, args ...any) *Script {
	s.notes = append(s.notes, fmt.Sprintf(format, args...))
	return s
}

// Empty reports whether the script would do nothing.
func (s *Script) Empty() bool {
	return len(s.exports) == 0 && len(s.unsets) == 0 && len(s.notes) == 0
}

// String renders the script as POSIX shell. Values are single-quoted, since
// profile and configuration names come from files on disk and could otherwise
// carry characters the shell would interpret.
func (s *Script) String() string {
	var b strings.Builder

	names := make([]string, 0, len(s.unsets))
	for n := range s.unsets {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "unset %s\n", n)
	}

	names = names[:0]
	for n := range s.exports {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "export %s=%s\n", n, quote(s.exports[n]))
	}

	for _, note := range s.notes {
		fmt.Fprintf(&b, "printf '%%s\\n' %s\n", quote(note))
	}
	return b.String()
}

// Quote exposes the same shell-safe quoting used for emitted assignments, for
// callers that need to build a line by hand.
func Quote(v string) string { return quote(v) }

// quote wraps a value in single quotes, which suppress every form of shell
// expansion. An embedded single quote is closed, escaped, and reopened -- the
// standard POSIX idiom, since single-quoted strings have no escape character.
func quote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}
