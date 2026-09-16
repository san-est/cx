package cloud

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseINISkipsNestedSubProperties(t *testing.T) {
	// The AWS CLI allows indented sub-properties under a key. A naive parser
	// reads them as top-level keys and corrupts the section.
	p := writeTemp(t, "config", `
[profile app]
region = eu-central-1
s3 =
    max_concurrent_requests = 20
    max_queue_size = 10000
output = json
`)
	f, err := parseINI(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.get("profile app", "region"); got != "eu-central-1" {
		t.Errorf("region = %q, want eu-central-1", got)
	}
	if got := f.get("profile app", "output"); got != "json" {
		t.Errorf("output = %q, want json (parser lost its place in the section)", got)
	}
	if got := f.get("profile app", "max_concurrent_requests"); got != "" {
		t.Errorf("sub-property leaked to top level as %q", got)
	}
}

func TestParseINIMissingFileIsNotAnError(t *testing.T) {
	// A machine may have ~/.aws/config without ~/.aws/credentials, or neither.
	f, err := parseINI(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("missing file returned error: %v", err)
	}
	if len(f) != 0 {
		t.Errorf("expected empty result, got %d sections", len(f))
	}
}

func TestParseINIHandlesCommentsAndValuesWithEquals(t *testing.T) {
	p := writeTemp(t, "config", `
# a comment
; another
[profile x]
credential_process = /usr/bin/helper --flag a=b
`)
	f, err := parseINI(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "/usr/bin/helper --flag a=b"
	if got := f.get("profile x", "credential_process"); got != want {
		t.Errorf("credential_process = %q, want %q", got, want)
	}
}
