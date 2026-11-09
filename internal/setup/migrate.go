// Copyright 2026 Canonical Inc.
// Originally by Gustavo Niemeyer.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/canonical/now/internal/cnfembed"
)

// legacyLine is one line of an old .now file: either a key=value
// pair, or a comment or blank line kept as written.
type legacyLine struct {
	key     string // empty for a comment or blank line
	value   string
	comment string // the comment text; "" when blank
}

// parseLegacy reads the old key=value format: blank lines and
// #-comments are kept, pairs are trimmed, anything else is an error.
func parseLegacy(path string, data []byte) ([]legacyLine, error) {
	var lines []legacyLine
	text := strings.TrimSuffix(string(data), "\n")
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			lines = append(lines, legacyLine{comment: ""})
			continue
		}
		if strings.HasPrefix(line, "#") {
			lines = append(lines, legacyLine{comment: line})
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("cannot parse %s: malformed line %q: expected key=value", path, line)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "api-url", "api-key", "api-model", "api-type":
		default:
			return nil, fmt.Errorf("cannot parse %s: unknown key %q", path, key)
		}
		lines = append(lines, legacyLine{key: key, value: value})
	}
	return lines, nil
}

// renderLegacy spells the parsed old file as cnf: the endpoint
// pairs and the comments land inside a providers.local block, and
// the old api-model value becomes the models.default
// provider-model. One blank line separates the two blocks.
func renderLegacy(lines []legacyLine) []byte {
	var b strings.Builder
	b.WriteString("providers.local:\n")
	for _, ln := range lines {
		switch {
		case ln.key == "api-model":
			// read into models.default below
		case ln.key != "":
			fmt.Fprintf(&b, "    %s: \"%s\"\n", ln.key, cnfembed.Escape(ln.value))
		case ln.comment != "":
			fmt.Fprintf(&b, "    %s\n", strings.TrimLeft(ln.comment, " \t"))
		}
	}
	b.WriteString("\nmodels.default:\n")
	b.WriteString("    provider: \"local\"\n")
	for _, ln := range lines {
		if ln.key == "api-model" && ln.value != "" {
			fmt.Fprintf(&b, "    provider-model: \"%s\"\n", cnfembed.Escape(ln.value))
		}
	}
	return []byte(b.String())
}

// migrate converts the old key=value file at oldPath into a cnf
// file at newPath, resolves the given model name - always
// models.default today, the only entry a converted file holds -
// and replaces the old file with a migration note once the new one
// is fully written and verified. On any failure the old file is
// left as it was.
func migrate(oldPath, newPath, model string) (*Options, error) {
	fail := func(format string, args ...any) (*Options, error) {
		return nil, fmt.Errorf("cannot migrate %s to %s: %s", oldPath, newPath, fmt.Sprintf(format, args...))
	}

	data, err := os.ReadFile(oldPath)
	if err != nil {
		return fail("cannot read old file: %v", err)
	}
	lines, err := parseLegacy(oldPath, data)
	if err != nil {
		return nil, err
	}
	oldOpts := legacyOptions(lines)
	if oldOpts.APIURL == "" {
		return fail("providers.local has no api-url")
	}

	cnfData := renderLegacy(lines)

	// Parse the candidate before it replaces anything, and check it
	// against the old file's own values.
	newOpts, err := parseConfig(newPath, model, cnfData)
	if err != nil {
		return fail("converted file does not parse: %v", err)
	}
	if !reflect.DeepEqual(newOpts, oldOpts) {
		return fail("converted file does not match the old configuration")
	}

	// Write the verified content to a temporary file next to the
	// target, then move it into place and leave a note where the old
	// file was. The parent directory is created on demand: the
	// default location nests under the configuration directory.
	if err := os.MkdirAll(filepath.Dir(newPath), 0o700); err != nil {
		return fail("cannot create configuration directory: %v", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(newPath), ".now-migrate-*")
	if err != nil {
		return fail("cannot create temporary file: %v", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(cnfData); err != nil {
		tmp.Close()
		return fail("cannot write temporary file: %v", err)
	}
	if err := tmp.Close(); err != nil {
		return fail("cannot close temporary file: %v", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fail("cannot set permissions: %v", err)
	}
	if err := os.Rename(tmpName, newPath); err != nil {
		return fail("cannot move temporary file into place: %v", err)
	}
	// The new file is in place. Leave a note in the old file instead
	// of deleting it, pointing at where the configuration now lives.
	// A failure here is harmless: the new file is already in place and
	// wins from now on, so at worst a stale legacy file remains.
	note := fmt.Sprintf("# Configuration migrated: %s\n", newPath)
	_ = os.WriteFile(oldPath, []byte(note), 0o600)
	return newOpts, nil
}

// legacyOptions reads the parsed legacy lines into Options, with
// the same defaults the cnf loader applies. An empty value counts
// as unset, so the converted file round-trips to the same result.
func legacyOptions(lines []legacyLine) *Options {
	opts := &Options{APIModel: "default", APIType: "completions-v1"}
	for _, ln := range lines {
		if ln.key == "" || ln.value == "" {
			continue
		}
		switch ln.key {
		case "api-url":
			opts.APIURL = ln.value
		case "api-key":
			opts.APIKey = ln.value
		case "api-model":
			opts.APIModel = ln.value
		case "api-type":
			opts.APIType = ln.value
		}
	}
	return opts
}
