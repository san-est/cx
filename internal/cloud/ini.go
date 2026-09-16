package cloud

import (
	"bufio"
	"os"
	"strings"
)

// iniFile is a minimal, tolerant INI reader built for the AWS CLI's config
// format. It deliberately does not use a general-purpose library: the AWS
// config file allows nested sub-properties (an indented block under a key like
// `s3 =`) that trip up strict parsers, and we only ever need top-level keys.
//
// Section names are stored verbatim; callers are responsible for stripping the
// "profile " prefix that ~/.aws/config uses but ~/.aws/credentials does not.
type iniFile map[string]map[string]string

// parseINI reads path and returns its sections. A missing file is not an
// error -- it yields an empty result, since a machine may legitimately have
// ~/.aws/config without ~/.aws/credentials, or neither.
func parseINI(path string) (iniFile, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return iniFile{}, nil
		}
		return nil, err
	}
	defer f.Close()

	out := iniFile{}
	section := ""

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		raw := sc.Text()

		// An indented line is a sub-property of the previous key. We have no
		// use for those, and parsing them as top-level keys would corrupt the
		// section, so skip before trimming.
		if strings.TrimSpace(raw) != "" && (raw[0] == ' ' || raw[0] == '\t') {
			continue
		}

		line := strings.TrimSpace(raw)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}

		if line[0] == '[' {
			if end := strings.IndexByte(line, ']'); end > 0 {
				section = strings.TrimSpace(line[1:end])
				if _, ok := out[section]; !ok {
					out[section] = map[string]string{}
				}
			}
			continue
		}

		if section == "" {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if key != "" {
			out[section][key] = val
		}
	}
	return out, sc.Err()
}

// get returns the value of key in section, or "" if either is absent.
func (f iniFile) get(section, key string) string {
	if s, ok := f[section]; ok {
		return s[key]
	}
	return ""
}
