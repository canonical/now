// Copyright (c) 2026 Canonical Inc.
// Originally by Gustavo Niemeyer.
// Use of source code is governed by the MIT-style license in the LICENSE file.

package setup_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/niemeyer/now/internal/setup"
)

func assertEqual[T any](t *testing.T, label string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", label, got, want)
	}
}

// withHome points $HOME at a temp dir for the test and restores it after.
func withHome(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if content != "" {
		if err := os.WriteFile(filepath.Join(dir, ".now"), []byte(content), 0o600); err != nil {
			t.Fatalf("writing config: %v", err)
		}
	}
	t.Setenv("HOME", dir)
	return dir
}

func TestLoadMissingFile(t *testing.T) {
	withHome(t, "")
	_, err := setup.Load()
	if err == nil || !strings.Contains(err.Error(), "cannot open") {
		t.Fatalf("expected open error, got %v", err)
	}
}

func TestLoadMissingAPIURL(t *testing.T) {
	withHome(t, "api-key=abc...\n")
	_, err := setup.Load()
	if err == nil || !strings.Contains(err.Error(), "api-url is not set") {
		t.Fatalf("expected api-url error, got %v", err)
	}
}

func TestLoadFullConfig(t *testing.T) {
	withHome(t, "api-url=http://127.0.0.1:11434\napi-key=abc...\napi-model=foobar\n")
	cfg, err := setup.Load()
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
	withHome(t, "api-url=http://127.0.0.1:11434\n")
	cfg, err := setup.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "APIModel", cfg.APIModel, "default")
	assertEqual(t, "APIType", cfg.APIType, "completions-v1")
	assertEqual(t, "APIKey", cfg.APIKey, "")
}

func TestLoadUnsupportedAPIType(t *testing.T) {
	withHome(t, "api-url=http://x\napi-type=other\n")
	_, err := setup.Load()
	if err == nil || !strings.Contains(err.Error(), "unsupported api-type") {
		t.Fatalf("expected api-type error, got %v", err)
	}
}

func TestLoadWhitespaceAndComments(t *testing.T) {
	withHome(t, "# comment\n\n  api-url = http://x  \n")
	cfg, err := setup.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "APIURL", cfg.APIURL, "http://x")
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"malformed line", "api-url\n", "malformed line"},
		{"unknown key", "api-toke=x\n", "unknown key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withHome(t, tt.content)
			_, err := setup.Load()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestLoadFrom(t *testing.T) {
	// LoadFrom reads an arbitrary path, independent of $HOME.
	path := filepath.Join(t.TempDir(), "custom-now")
	content := "api-url=http://127.0.0.1:1234\napi-model=mymodel\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := setup.LoadFrom(path)
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
	_, err := setup.LoadFrom(path)
	if err == nil || !strings.Contains(err.Error(), "cannot open "+path) {
		t.Fatalf("expected open error for %s, got %v", path, err)
	}
}