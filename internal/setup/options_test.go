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

package setup_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/canonical/now/internal/setup"
)

func assertEqual[T any](t *testing.T, label string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", label, got, want)
	}
}

// withConfig points $XDG_CONFIG_HOME at a temp dir for the test and
// restores it after, writing the given cnf content to now/now.cnf.
// NOW_HOME is cleared so the default location is the one under test
// even when the environment sets it.
func withConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if content != "" {
		if err := os.MkdirAll(filepath.Join(dir, "now"), 0o700); err != nil {
			t.Fatalf("creating config dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "now", "now.cnf"), []byte(content), 0o600); err != nil {
			t.Fatalf("writing config: %v", err)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("NOW_HOME", "")
	return dir
}

// localConfig is the minimal working configuration: local provider,
// default model on it.
func localConfig(extra string) string {
	return "providers.local:\n" +
		"    api-url: \"http://127.0.0.1:11434\"\n" +
		extra +
		"\nmodels.default:\n" +
		"    provider: \"local\"\n"
}

func TestLoadMissingFile(t *testing.T) {
	withConfig(t, "")
	_, err := setup.Load("")
	if err == nil || !strings.Contains(err.Error(), "cannot open") {
		t.Fatalf("expected open error, got %v", err)
	}
}

func TestLoadMissingModelDefault(t *testing.T) {
	withConfig(t, "providers.local:\n    api-url: \"http://x\"\n")
	_, err := setup.Load("")
	if err == nil || !strings.Contains(err.Error(), "models.default not found") {
		t.Fatalf("expected models.default error, got %v", err)
	}
}

func TestLoadModelWithoutProvider(t *testing.T) {
	withConfig(t, "providers.local:\n    api-url: \"http://x\"\n\nmodels.default: {}\n")
	_, err := setup.Load("")
	if err == nil || !strings.Contains(err.Error(), "models.default has no provider") {
		t.Fatalf("expected provider error, got %v", err)
	}
}

func TestLoadUnknownProvider(t *testing.T) {
	withConfig(t, "providers.local:\n    api-url: \"http://x\"\n\nmodels.default:\n    provider: \"remote\"\n")
	_, err := setup.Load("")
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("expected unknown provider error, got %v", err)
	}
}

func TestLoadMissingAPIURL(t *testing.T) {
	withConfig(t, "providers.local:\n    api-key: \"abc...\"\n\nmodels.default:\n    provider: \"local\"\n")
	_, err := setup.Load("")
	want := `providers."local" has no api-url`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error containing %q, got %v", want, err)
	}
}

func TestLoadFullConfig(t *testing.T) {
	withConfig(t, "providers.local:\n"+
		"    api-url: \"http://127.0.0.1:11434\"\n"+
		"    api-key: \"abc...\"\n"+
		"    api-type: \"completions-v1\"\n"+
		"\nmodels.default:\n"+
		"    provider: \"local\"\n"+
		"    provider-model: \"foobar\"\n")
	cfg, err := setup.Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Options", *cfg, setup.Options{
		APIURL:   "http://127.0.0.1:11434",
		APIKey:   "abc...",
		APIModel: "foobar",
		APIType:  "completions-v1",
	})
}

func TestLoadDefaults(t *testing.T) {
	withConfig(t, localConfig(""))
	cfg, err := setup.Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "APIModel", cfg.APIModel, "default")
	assertEqual(t, "APIType", cfg.APIType, "completions-v1")
	assertEqual(t, "APIKey", cfg.APIKey, "")
}

func TestLoadUnsupportedAPIType(t *testing.T) {
	withConfig(t, localConfig("    api-type: \"other\"\n"))
	_, err := setup.Load("")
	if err == nil || !strings.Contains(err.Error(), "unsupported api-type") {
		t.Fatalf("expected api-type error, got %v", err)
	}
}

func TestLoadComments(t *testing.T) {
	withConfig(t, "# comment\n\nproviders.local:\n    api-url: \"http://x\"\n\nmodels.default:\n    provider: \"local\"\n")
	cfg, err := setup.Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "APIURL", cfg.APIURL, "http://x")
}

func TestLoadExtraBlocks(t *testing.T) {
	// Blocks beyond the resolved pair load fine alongside them.
	withConfig(t, localConfig("")+
		"providers.remote:\n"+
		"    api-url: \"http://remote\"\n"+
		"\nmodels.other:\n"+
		"    provider: \"remote\"\n"+
		"    provider-model: \"extra-model\"\n")
	cfg, err := setup.Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "APIURL", cfg.APIURL, "http://127.0.0.1:11434")
	assertEqual(t, "APIModel", cfg.APIModel, "default")
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"bare word value", "api-url\n", "unquoted value"},
		{"unknown top-level key", "api-toke: \"x\"\n", "unknown field"},
		{"unknown provider key", "providers.local:\n    api-toke: \"x\"\n", "unknown field"},
		{"unknown model name", "models.default:\n    provider: \"x\"\n    toke: \"y\"\n", "unknown field"},
		{"cnf syntax error", "\t\tapi-url: \"x\"\n", "indentation prefixes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withConfig(t, tt.content)
			_, err := setup.Load("")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestLoadNowHome(t *testing.T) {
	// NOW_HOME holds a directory whose now.cnf takes precedence over
	// the default location.
	withConfig(t, localConfig(""))
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating dir: %v", err)
	}
	content := "providers.local:\n" +
		"    api-url: \"http://127.0.0.1:9999\"\n" +
		"\nmodels.default:\n" +
		"    provider: \"local\"\n" +
		"    provider-model: \"altmodel\"\n"
	if err := os.WriteFile(filepath.Join(dir, "now.cnf"), []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv("NOW_HOME", dir)

	cfg, err := setup.Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "APIURL", cfg.APIURL, "http://127.0.0.1:9999")
	assertEqual(t, "APIModel", cfg.APIModel, "altmodel")
}

func TestLoadNowHomeEmptyFallsBackToDefault(t *testing.T) {
	// An empty NOW_HOME is as good as unset.
	withConfig(t, localConfig(""))
	t.Setenv("NOW_HOME", "")

	cfg, err := setup.Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "APIURL", cfg.APIURL, "http://127.0.0.1:11434")
}

func TestLoadNowHomeMissing(t *testing.T) {
	// A missing now.cnf under NOW_HOME is an error; there is no
	// silent fallback to the default location.
	withConfig(t, localConfig(""))
	dir := t.TempDir()
	t.Setenv("NOW_HOME", dir)

	_, err := setup.Load("")
	want := filepath.Join(dir, "now.cnf")
	if err == nil || !strings.Contains(err.Error(), "cannot open "+want) {
		t.Fatalf("expected open error for %s, got %v", want, err)
	}
}

func TestDefaultPath(t *testing.T) {
	dir := withConfig(t, "")
	path, err := setup.DefaultPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "DefaultPath", path, filepath.Join(dir, "now", "now.cnf"))
}

func TestDefaultPathNowHome(t *testing.T) {
	withConfig(t, "")
	dir := t.TempDir()
	t.Setenv("NOW_HOME", dir)

	got, err := setup.DefaultPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "DefaultPath", got, filepath.Join(dir, "now.cnf"))
}

func TestDefaultPathXDGUnset(t *testing.T) {
	// An empty XDG_CONFIG_HOME is as good as unset: the default
	// nests under ~/.config.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("NOW_HOME", "")

	got, err := setup.DefaultPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "DefaultPath", got, filepath.Join(home, ".config", "now", "now.cnf"))
}

func TestLoadFrom(t *testing.T) {
	// LoadFrom reads an arbitrary path, independent of $HOME.
	path := filepath.Join(t.TempDir(), "custom-.now.cnf")
	content := "providers.local:\n" +
		"    api-url: \"http://127.0.0.1:1234\"\n" +
		"\nmodels.default:\n" +
		"    provider: \"local\"\n" +
		"    provider-model: \"mymodel\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := setup.LoadFrom(path, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Options", *cfg, setup.Options{
		APIURL:   "http://127.0.0.1:1234",
		APIModel: "mymodel",
		APIType:  "completions-v1",
	})
}

func TestLoadFromMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing")
	_, err := setup.LoadFrom(path, "")
	if err == nil || !strings.Contains(err.Error(), "cannot open "+path) {
		t.Fatalf("expected open error for %s, got %v", path, err)
	}
}

func TestLoadModelByKey(t *testing.T) {
	content := "providers.local:\n" +
		"    api-url: \"http://local\"\n" +
		"\nproviders.remote:\n" +
		"    api-url: \"http://remote\"\n" +
		"    api-key: \"rk\"\n" +
		"\nmodels.default:\n" +
		"    provider: \"local\"\n" +
		"\nmodels.other:\n" +
		"    provider: \"remote\"\n" +
		"    provider-model: \"big-model\"\n"
	withConfig(t, content)
	tests := []struct {
		model string
		url   string
		key   string
		name  string
	}{
		{"", "http://local", "", "default"},
		{"other", "http://remote", "rk", "big-model"},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			cfg, err := setup.Load(tt.model)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertEqual(t, "APIURL", cfg.APIURL, tt.url)
			assertEqual(t, "APIKey", cfg.APIKey, tt.key)
			assertEqual(t, "APIModel", cfg.APIModel, tt.name)
		})
	}
}

func TestLoadUnknownModelKey(t *testing.T) {
	withConfig(t, localConfig(""))
	_, err := setup.Load("nope")
	want := "models.nope not found"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error containing %q, got %v", want, err)
	}
}
