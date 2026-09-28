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

// Package setup loads the now configuration from $HOME/.now.
package setup

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Options holds the settings used to reach the model API.
type Options struct {
	APIURL   string
	APIKey   string
	APIModel string
	// APIType selects the API kind; only "completions-v1" (the completions API)
	// is supported for now.
	APIType string
}

// Load reads the configuration from $HOME/.now. Missing keys get their
// defaults; api-url is required.
func Load() (*Options, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot discover home directory: %w", err)
	}
	return LoadFrom(filepath.Join(home, ".now"))
}

// DefaultPath is the configuration file path, $HOME/.now.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot discover home directory: %w", err)
	}
	return filepath.Join(home, ".now"), nil
}

// LoadFrom reads the configuration from the given file path.
func LoadFrom(path string) (*Options, error) {
	opts := &Options{APIModel: "default", APIType: "completions-v1"}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("cannot parse %s: malformed line %q: expected key=value", path, line)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "api-url":
			opts.APIURL = value
		case "api-key":
			opts.APIKey = value
		case "api-model":
			opts.APIModel = value
		case "api-type":
			opts.APIType = value
		default:
			return nil, fmt.Errorf("cannot parse %s: unknown key %q", path, key)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	if opts.APIURL == "" {
		return nil, fmt.Errorf("cannot load %s: api-url is not set", path)
	}
	if opts.APIType != "completions-v1" {
		return nil, fmt.Errorf("cannot load %s: unsupported api-type %q", path, opts.APIType)
	}
	return opts, nil
}