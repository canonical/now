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

package busybox_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/canonical/now/internal/busybox"
)

func assertEqual[T any](t *testing.T, label string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", label, got, want)
	}
}

// fakeBusybox writes an executable named busybox into a temp dir and adds
// that dir to the front of $PATH, so Probe resolves the fake.
func fakeBusybox(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "busybox")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("cannot write fake busybox: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

func TestProbeResolvesBusybox(t *testing.T) {
	fakeBusybox(t, "#!/bin/sh\necho sh ls cat\n")
	opts, err := busybox.Probe(busybox.Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filepath.IsAbs(opts.Path) {
		t.Errorf("path %q is not absolute", opts.Path)
	}
	assertEqual(t, "applets", opts.Applets, []string{"sh", "ls", "cat"})
}

func TestProbeDropsBracketApplets(t *testing.T) {
	// The [ and [[ applets are dropped: they are shell builtins the
	// model knows well, so listing them in the prompt is pure noise.
	// static-sh is dropped too: an alias for the shell itself. Other
	// applets stay listed — the model is told to use only the allowed
	// commands, so removing one it might legitimately emit risks it
	// avoiding valid shell.
	fakeBusybox(t, "#!/bin/sh\necho sh [ [[ static-sh cat\n")
	opts, err := busybox.Probe(busybox.Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "applets", opts.Applets, []string{"sh", "cat"})
}

func TestProbeKeepsFilledOptions(t *testing.T) {
	// An already-resolved Options is returned as is, so probing twice
	// does not re-run busybox --list.
	want := busybox.Options{Path: "/bin/busybox", Applets: []string{"sh"}}
	got, err := busybox.Probe(want)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "options", got, want)
}

func TestProbeMissingBusybox(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	_, err := busybox.Probe(busybox.Options{})
	if err == nil || !strings.Contains(err.Error(), "cannot find busybox in $PATH") {
		t.Fatalf("expected missing busybox error, got %v", err)
	}
}

func TestProbeListFailure(t *testing.T) {
	fakeBusybox(t, "#!/bin/sh\necho oops >&2\nexit 1\n")
	_, err := busybox.Probe(busybox.Options{})
	if err == nil || !strings.Contains(err.Error(), "cannot list busybox applets") {
		t.Fatalf("expected list failure error, got %v", err)
	}
}

func TestProbeEmptyList(t *testing.T) {
	// A silent --list is as good as no list: the prompt would promise
	// the model applets that do not exist.
	fakeBusybox(t, "#!/bin/sh\nexit 0\n")
	_, err := busybox.Probe(busybox.Options{})
	if err == nil || !strings.Contains(err.Error(), "cannot list busybox applets: empty output") {
		t.Fatalf("expected empty output error, got %v", err)
	}
}

func TestProbeRealBusybox(t *testing.T) {
	// Against the real busybox when present, covering the actual
	// integration the CLI depends on.
	if _, err := exec.LookPath("busybox"); err != nil {
		t.Skip("busybox not available in $PATH")
	}
	opts, err := busybox.Probe(busybox.Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, a := range opts.Applets {
		if a == "sh" {
			found = true
		}
	}
	if !found {
		t.Errorf("applet list %v does not include sh", opts.Applets)
	}
}
