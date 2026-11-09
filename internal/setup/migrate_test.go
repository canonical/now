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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/now/internal/setup"
)

// withLegacy writes an old key=value .now file into a fresh temp
// home and points $HOME there, with $XDG_CONFIG_HOME at a second
// temp dir holding no configuration. NOW_HOME is cleared so the
// default location is the one under test even when the environment
// sets it. It returns the home and the XDG configuration directories.
func withLegacy(t *testing.T, content string) (home, xdg string) {
	t.Helper()
	home = t.TempDir()
	xdg = t.TempDir()
	if content != "" {
		if err := os.WriteFile(filepath.Join(home, ".now"), []byte(content), 0o600); err != nil {
			t.Fatalf("writing legacy config: %v", err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("NOW_HOME", "")
	return home, xdg
}

func TestMigrate(t *testing.T) {
	home, xdg := withLegacy(t, ""+
		"# main settings\n"+
		"api-url=http://127.0.0.1:11434\n"+
		"\n"+
		"  # indented comment\n"+
		"api-key=abc...\n"+
		"api-model=foobar\n")

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

	newPath := filepath.Join(xdg, "now", "now.cnf")
	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("reading migrated config: %v", err)
	}
	want := "" +
		"providers.local:\n" +
		"    # main settings\n" +
		"    api-url: \"http://127.0.0.1:11434\"\n" +
		"    # indented comment\n" +
		"    api-key: \"abc...\"\n" +
		"\n" +
		"models.default:\n" +
		"    provider: \"local\"\n" +
		"    provider-model: \"foobar\"\n"
	assertEqual(t, "migrated content", string(data), want)

	// The legacy file is not removed: it holds a note pointing at
	// where the configuration now lives.
	note, err := os.ReadFile(filepath.Join(home, ".now"))
	if err != nil {
		t.Fatalf("reading legacy file: %v", err)
	}
	assertEqual(t, "legacy note", string(note), fmt.Sprintf("# Configuration migrated: %s\n", newPath))

	info, err := os.Stat(newPath)
	if err != nil {
		t.Fatalf("stat migrated config: %v", err)
	}
	assertEqual(t, "permissions", info.Mode().Perm(), os.FileMode(0o600))
}

func TestMigrateNewFileWins(t *testing.T) {
	home, xdg := withLegacy(t, "api-url=http://old\n")
	newContent := "providers.local:\n" +
		"    api-url: \"http://new\"\n" +
		"\nmodels.default:\n" +
		"    provider: \"local\"\n"
	if err := os.MkdirAll(filepath.Join(xdg, "now"), 0o700); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "now", "now.cnf"), []byte(newContent), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	cfg, err := setup.Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "APIURL", cfg.APIURL, "http://new")
	// The legacy file must be left alone, content included, when the
	// new file already exists.
	data, err := os.ReadFile(filepath.Join(home, ".now"))
	if err != nil {
		t.Fatalf("reading legacy file: %v", err)
	}
	assertEqual(t, "legacy content", string(data), "api-url=http://old\n")
}

func TestMigrateBadLegacy(t *testing.T) {
	home, xdg := withLegacy(t, "api-url\n")
	_, err := setup.Load("")
	if err == nil || !strings.Contains(err.Error(), "expected key=value") {
		t.Fatalf("expected legacy parse error, got %v", err)
	}
	if _, err2 := os.Stat(filepath.Join(home, ".now")); err2 != nil {
		t.Fatalf("legacy file must be kept on failure: %v", err2)
	}
	if _, err2 := os.Stat(filepath.Join(xdg, "now", "now.cnf")); !os.IsNotExist(err2) {
		t.Fatalf("failed migration must not create the new config")
	}
}

func TestMigrateUnknownKey(t *testing.T) {
	home, _ := withLegacy(t, "api-url=http://x\napi-toke=y\n")
	_, err := setup.Load("")
	if err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("expected unknown key error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".now")); err != nil {
		t.Fatalf("legacy file must be kept on failure: %v", err)
	}
}

func TestMigrateMissingAPIURL(t *testing.T) {
	home, _ := withLegacy(t, "api-key=abc...\n")
	_, err := setup.Load("")
	if err == nil || !strings.Contains(err.Error(), "has no api-url") {
		t.Fatalf("expected api-url error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".now")); err != nil {
		t.Fatalf("legacy file must be kept on failure: %v", err)
	}
}
