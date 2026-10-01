package cloud

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// iniSeeds are shapes real AWS and gcloud files take, plus the edge cases the
// hand-written parser and writer each have to get right.
var iniSeeds = []string{
	"",
	"\n",
	"[default]\nregion = us-east-1\n",
	"[profile app]\nregion = eu-central-1\ns3 =\n    max_concurrent_requests = 20\n    max_queue_size = 10000\noutput = json\n",
	"# comment\n; comment\n[profile x]\ncredential_process = /usr/bin/helper --flag a=b\n",
	"[a]\nk = 1\n\n[b]\nk = 2\n",
	"[a]\nk = 1\n[a]\nk = 2\n",
	"[a]\r\nk = 1\r\n\r\n[b]\r\nk = 2\r\n",
	"[a]\nk = 1",
	"preamble = x\n[a]\nk = 1\n",
	"[a] ; trailing comment\nk = 1\n",
	"[ a ]\nk = 1\n",
	"[a]\ns3 =\n  [b]\nk = 1\n",
	"[]\nk = 1\n",
	"[a\nk = 1\n",
	"[sso-session corp]\nsso_start_url = https://corp.awsapps.com/start\nsso_region = eu-west-1\n",
}

// FuzzParseINI checks that the parser never panics on arbitrary input, and that
// whatever it does return survives being written back out and read again.
func FuzzParseINI(f *testing.F) {
	for _, s := range iniSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data string) {
		dir := t.TempDir()
		first, err := parseINI(writeFuzzFile(t, dir, "in", data))
		if err != nil {
			// Only a line longer than the scanner's buffer can do this, and an
			// error is the right answer to it.
			return
		}

		second, err := parseINI(writeFuzzFile(t, dir, "out", renderINI(first)))
		if err != nil {
			t.Fatalf("re-parsing rendered output: %v", err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("round trip changed the result\nfirst:  %#v\nsecond: %#v", first, second)
		}
	})
}

// renderINI writes sections in a canonical layout: sorted, one key per line, no
// comments or sub-properties.
func renderINI(f iniFile) string {
	var b strings.Builder
	for _, name := range sortedKeys(f) {
		b.WriteString("[" + name + "]\n")
		for _, k := range sortedKeys(f[name]) {
			b.WriteString(k + " = " + f[name][k] + "\n")
		}
	}
	return b.String()
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// fuzzKey is the shape of every key cx writes.
var fuzzKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// FuzzUpsertINISection checks the property the line editor exists for: writing
// one section leaves every other section byte for byte as it was, including the
// indented sub-properties a parse-and-rewrite would drop.
func FuzzUpsertINISection(f *testing.F) {
	for _, s := range iniSeeds {
		for _, section := range []string{"a", "default", "profile app", "sso-session corp"} {
			f.Add(s, section, "region", "eu-west-1")
		}
	}
	for _, v := range []string{"x\n[evil]\nk = 1", "x\r\nk = 1", "x\r"} {
		f.Add("[a]\nk = 1\n", "a", "region", v)
	}
	f.Fuzz(func(t *testing.T, data, section, key, value string) {
		// Only inputs cx can actually produce: every caller passes a validated
		// name, under at most one of the prefixes the AWS config file uses, and
		// a fixed key.
		name := strings.TrimPrefix(strings.TrimPrefix(section, "profile "), "sso-session ")
		if ValidateName(name) != nil || !fuzzKey.MatchString(key) {
			return
		}
		p := writeFuzzFile(t, t.TempDir(), "credentials", data)
		if strings.ContainsAny(value, "\r\n") {
			if err := upsertINISection(p, section, map[string]string{key: value}); err == nil {
				t.Fatalf("value %q with a line break was accepted", value)
			}
			if got := readFuzzFile(t, p); got != data {
				t.Fatalf("rejected write still changed the file\ninput:  %q\noutput: %q", data, got)
			}
			return
		}
		if value != strings.TrimSpace(value) {
			return
		}

		if _, err := parseINI(p); err != nil {
			// A line too long for the parser; nothing to compare against.
			return
		}
		kv := map[string]string{key: value}
		if err := upsertINISection(p, section, kv); err != nil {
			return
		}
		got := readFuzzFile(t, p)

		assertOthersUnchanged(t, section, data, got)

		// The write took effect, as the parser sees it.
		parsed, err := parseINI(p)
		if err != nil {
			t.Fatalf("parsing written file: %v", err)
		}
		if v := parsed.get(section, key); v != value {
			t.Fatalf("%s.%s = %q after writing %q\ninput:\n%q\noutput:\n%q",
				section, key, v, value, data, got)
		}

		// Writing the same thing again changes nothing.
		if err := upsertINISection(p, section, kv); err != nil {
			t.Fatalf("second write: %v", err)
		}
		if again := readFuzzFile(t, p); again != got {
			t.Fatalf("second write was not a no-op\nfirst:  %q\nsecond: %q", got, again)
		}
	})
}

// FuzzRemoveINISection checks the same property for deletion: removing one
// section leaves every other section byte for byte as it was.
func FuzzRemoveINISection(f *testing.F) {
	for _, s := range iniSeeds {
		for _, section := range []string{"a", "b", "default", "profile app"} {
			f.Add(s, section)
		}
	}
	f.Fuzz(func(t *testing.T, data, section string) {
		name := strings.TrimPrefix(strings.TrimPrefix(section, "profile "), "sso-session ")
		if ValidateName(name) != nil {
			return
		}

		p := writeFuzzFile(t, t.TempDir(), "credentials", data)
		removed, err := removeINISection(p, section)
		if err != nil {
			t.Fatalf("remove: %v", err)
		}
		got := readFuzzFile(t, p)
		if !removed {
			if got != data {
				t.Fatalf("nothing removed, but the file changed\ninput:  %q\noutput: %q", data, got)
			}
			return
		}

		assertOthersUnchanged(t, section, data, got)
		if n := countHeaders(got, section); n != 0 {
			t.Fatalf("%d headers named %q left after removal\ninput:  %q\noutput: %q",
				n, section, data, got)
		}
	})
}

// countHeaders returns how many times a section header named section appears.
func countHeaders(data, section string) int {
	n := 0
	for _, line := range strings.SplitAfter(data, "\n") {
		if name, ok := iniHeader(line); ok && name == section {
			n++
		}
	}
	return n
}

// assertOthersUnchanged fails unless every section other than section reads
// the same in after as in before, byte for byte.
func assertOthersUnchanged(t *testing.T, section, before, after string) {
	t.Helper()
	was, now := splitSections(before), splitSections(after)
	delete(was, "["+section)
	delete(now, "["+section)
	for name, want := range was {
		// A missing segment reads as empty, which only passes below if all it
		// held was blank lines.
		have := now[name]
		if have != want && !differOnlyInTrailingBlankLines(want, have) {
			t.Fatalf("section %q changed\nbefore: %q\nafter:  %q\ninput:\n%q\noutput:\n%q",
				name, want, have, before, after)
		}
	}
	for name := range now {
		if _, ok := was[name]; !ok {
			t.Fatalf("section %q appeared\ninput:\n%q\noutput:\n%q", name, before, after)
		}
	}
}

// splitSections cuts a file into its sections' raw text, using the parser's
// rules rather than the writer's so the two are checked against each other.
// Sections are keyed "[" + name, which keeps a section literally named "" apart
// from the text before the first header, keyed "". A name that appears twice
// gets its occurrences joined, in order.
func splitSections(data string) map[string]string {
	out := map[string]string{}
	key := ""
	for _, line := range strings.SplitAfter(data, "\n") {
		if line == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			t := strings.TrimSpace(line)
			if t != "" && t[0] == '[' {
				if end := strings.IndexByte(t, ']'); end > 0 {
					key = "[" + strings.TrimSpace(t[1:end])
				}
			}
		}
		out[key] += line
	}
	return out
}

// differOnlyInTrailingBlankLines allows the edits the line editors may make to
// a neighbour: terminating an unterminated last line, and adding or taking the
// blank line that separates it from a section appended or removed after it.
// Blank here means what the parser means: whitespace only.
func differOnlyInTrailingBlankLines(a, b string) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	if !strings.HasPrefix(b, a) {
		return false
	}
	return strings.TrimSpace(b[len(a):]) == ""
}

func writeFuzzFile(t *testing.T, dir, name, data string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func readFuzzFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
