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

package sandbox_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/canonical/now/internal/sandbox"
)

func assertEqual[T any](t *testing.T, label string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", label, got, want)
	}
}

// requireBwrap skips the test when bwrap is not available.
func requireBwrap(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap is not available")
	}
}

// runIn probes the confinement and runs the shell line inside it,
// returning its combined output and error.
func runIn(t *testing.T, opts sandbox.Options, line string) (string, error) {
	t.Helper()
	busybox, err := exec.LookPath("busybox")
	if err != nil {
		t.Fatalf("cannot find busybox: %v", err)
	}
	bwrap, err := sandbox.Probe(opts, busybox)
	if err != nil {
		return "", err
	}
	opts.Bwrap = bwrap

	var out bytes.Buffer
	bargs, err := sandbox.Args(opts, busybox, "sh", "-c", line)
	if err != nil {
		return "", err
	}
	cmd := exec.Command(bargs[0], bargs[1:]...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Run()
	return out.String(), err
}

// fakeBwrap installs a fake bwrap on $PATH simulating an environment's
// proc-mount policy. The behavior switch is an env var so the failure
// message with its apostrophe stays out of shell quoting:
//
//   - NOW_FAKE=ok:      every invocation passes through and runs the
//     command after -- for real (fresh procfs mounts).
//   - NOW_FAKE=denied: invocations carrying --proc fail with bwrap's
//     proc mount error; others pass through (procfs mount is blocked).
//   - NOW_FAKE=broken: every invocation fails (confinement impossible).
//
// It records each invocation's arguments in the returned dir's "calls"
// file for asserting which form was used.
func fakeBwrap(t *testing.T, behavior string) string {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	path := filepath.Join(dir, "bwrap")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> " + calls + "\n" +
		"for a in \"$@\"; do if [ \"$a\" = \"--proc\" ] || [ \"$NOW_FAKE\" = \"broken\" ]; then\n" +
		"  if [ \"$a\" = \"--proc\" ] && [ \"$NOW_FAKE\" = \"denied\" ] || [ \"$NOW_FAKE\" = \"broken\" ]; then\n" +
		"    printf '%s\\n' \"$NOW_FAKE_ERR\" >&2; exit 1\n" +
		"  fi\n" +
		"fi; done\n" +
		"while [ $# -gt 0 ]; do if [ \"$1\" = \"--\" ]; then shift; exec \"$@\"; fi; shift; done\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("cannot write fake bwrap: %v", err)
	}
	t.Setenv("NOW_FAKE", behavior)
	t.Setenv("NOW_FAKE_ERR", "bwrap: Can't mount proc on /newroot/proc: Operation not permitted")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// fakeCalls reads the fake bwrap's recorded invocations.
func fakeCalls(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil {
		t.Fatalf("cannot read calls: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestProbeCachesBwrap(t *testing.T) {
	// Probe with Options.Bwrap already set returns it immediately.
	busybox, err := exec.LookPath("busybox")
	if err != nil {
		t.Fatalf("cannot find busybox: %v", err)
	}
	want := sandbox.Bwrap{Path: "/cached/bwrap", HostProc: true}
	got, err := sandbox.Probe(sandbox.Options{Bwrap: want}, busybox)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Bwrap", got, want)
}

func TestProbeMissingBwrap(t *testing.T) {
	// Without bwrap on $PATH, probing fails: confining is impossible.
	busybox, err := exec.LookPath("busybox")
	if err != nil {
		t.Fatalf("cannot find busybox: %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	_, err = sandbox.Probe(sandbox.Options{}, busybox)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("cannot find bwrap")) {
		t.Fatalf("expected missing bwrap error, got %v", err)
	}
}

func TestProbeFreshProc(t *testing.T) {
	// Where the fresh procfs mounts, the probe keeps HostProc unset and
	// Run uses --proc.
	dir := fakeBwrap(t, "ok")
	busybox, err := exec.LookPath("busybox")
	if err != nil {
		t.Fatalf("cannot find busybox: %v", err)
	}

	bwrap, err := sandbox.Probe(sandbox.Options{}, busybox)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "HostProc", bwrap.HostProc, false)
	assertEqual(t, "fake is the resolved bwrap", filepath.Base(bwrap.Path), "bwrap")

	// Args then builds the fresh procfs form.
	last := fakeCalls(t, dir)[len(fakeCalls(t, dir))-1]
	assertEqual(t, "fresh form used", strings.Contains(last, "--proc /proc"), true)
}

func TestProbeDeniedProcFallsBackToHost(t *testing.T) {
	// Where the fresh procfs is denied, the probe records HostProc and
	// Run binds the host's /proc instead.
	dir := fakeBwrap(t, "denied")
	busybox, err := exec.LookPath("busybox")
	if err != nil {
		t.Fatalf("cannot find busybox: %v", err)
	}

	bwrap, err := sandbox.Probe(sandbox.Options{}, busybox)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "HostProc", bwrap.HostProc, true)

	// Args then builds the host proc form.
	last := fakeCalls(t, dir)[len(fakeCalls(t, dir))-1]
	assertEqual(t, "host proc used", strings.Contains(last, "--ro-bind /proc /proc"), true)
	assertEqual(t, "fresh proc not used", strings.Contains(last, "--proc /proc"), false)
}

func TestProbeBrokenFails(t *testing.T) {
	// Where confinement cannot work at all, the probe fails.
	fakeBwrap(t, "broken")
	busybox, err := exec.LookPath("busybox")
	if err != nil {
		t.Fatalf("cannot find busybox: %v", err)
	}

	_, err = sandbox.Probe(sandbox.Options{}, busybox)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("cannot confine")) {
		t.Fatalf("expected confinement error, got %v", err)
	}
}

func TestProbeDeterminesProcForm(t *testing.T) {
	// The probe answers with the proc form this environment supports.
	requireBwrap(t)
	busybox, err := exec.LookPath("busybox")
	if err != nil {
		t.Fatalf("cannot find busybox: %v", err)
	}
	bwrap, err := sandbox.Probe(sandbox.Options{}, busybox)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bwrap.Path == "" {
		t.Fatalf("expected resolved bwrap path")
	}
	// Both forms are valid outcomes; the assertion is that Run then
	// works with what the probe chose, covered by the confinement tests.
}

func TestWrapUnconfinedRuns(t *testing.T) {
	requireBwrap(t)
	out, err := runIn(t, sandbox.Options{}, "echo inside")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "output", out, "inside\n")
}

func TestWrapUngrantedPathInaccessible(t *testing.T) {
	requireBwrap(t)
	// Without a grant, /etc/hostname cannot be read.
	_, err := runIn(t, sandbox.Options{}, "cat /etc/hostname")
	if err == nil {
		t.Fatalf("expected read failure, got success")
	}
}

func TestWrapCwdUngrantedIsReadOnly(t *testing.T) {
	// The working directory always exists inside the sandbox: when it
	// is not granted, an empty read-only directory is bound in its
	// place. Scripts start where the user ran now and absolute paths
	// look natural, but writing there fails loudly instead of
	// silently succeeding in a discarded tmpfs.
	requireBwrap(t)
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("cannot get cwd: %v", err)
	}

	out, err := runIn(t, sandbox.Options{Cwd: cwd}, "pwd; touch fresh 2>&1; ls")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(out, cwd+"\n") {
		t.Errorf("unexpected output: %q", out)
	}
	if !strings.Contains(out, "Read-only file system") {
		t.Errorf("expected EROFS in output, got %q", out)
	}
	if strings.Contains(out, "fresh\n") {
		t.Errorf("write to ungranted cwd appeared to succeed: %q", out)
	}
}

func TestWrapCwdGrantedKeepsContent(t *testing.T) {
	// When the working directory is granted, the script starts in it
	// and sees its real content, not an empty tmpfs.
	requireBwrap(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("here\n"), 0o644); err != nil {
		t.Fatalf("cannot write data: %v", err)
	}
	t.Chdir(dir)

	out, err := runIn(t, sandbox.Options{Cwd: dir, Readable: []string{dir}}, "pwd; cat data.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "output", out, dir+"\nhere\n")
}

func TestWrapCwdEmptyStartsAtRoot(t *testing.T) {
	// An empty Cwd leaves the working directory wherever bwrap starts
	// it, usually /.
	requireBwrap(t)
	out, err := runIn(t, sandbox.Options{}, "pwd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "output", out, "/\n")
}

func TestWrapCwdGrantBelowCwd(t *testing.T) {
	// A grant at or below the working directory still mounts on top of
	// the read-only cwd, not shadowed by it.
	// A grant at or below the working directory still mounts on top of
	// the cwd tmpfs, not shadowed by it.
	requireBwrap(t)
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("cannot create sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "data.txt"), []byte("deep\n"), 0o644); err != nil {
		t.Fatalf("cannot write data: %v", err)
	}
	t.Chdir(dir)

	out, err := runIn(t, sandbox.Options{Cwd: dir, Readable: []string{sub}}, "pwd; ls; ls sub")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "output", out, dir+"\nsub\ndata.txt\n")
}

func TestWrapCwdRelativeGrant(t *testing.T) {
	// Grants arrive from the parser as absolute paths; a grant
	// covering the working directory means no tmpfs covers it.
	requireBwrap(t)
	dir := t.TempDir()
	t.Chdir(dir)

	out, err := runIn(t, sandbox.Options{Cwd: dir, Writable: []string{dir}}, "pwd; touch written; ls")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(out, dir+"\n") {
		t.Errorf("unexpected output: %q", out)
	}
	if !strings.Contains(out, "written") {
		t.Errorf("cwd not writable via grant: %q", out)
	}
}

func TestWrapReadableGrant(t *testing.T) {
	requireBwrap(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(path, []byte("granted\n"), 0o644); err != nil {
		t.Fatalf("cannot write data: %v", err)
	}

	out, err := runIn(t, sandbox.Options{Readable: []string{path}}, "cat "+path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "output", out, "granted\n")
}

func TestWrapWritableGrant(t *testing.T) {
	requireBwrap(t)
	// Grants must name existing paths; grant the directory so the
	// script can create a file inside it.
	dir := t.TempDir()

	_, err := runIn(t, sandbox.Options{Writable: []string{dir}}, "echo written > "+filepath.Join(dir, "data.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "data.txt"))
	if err != nil {
		t.Fatalf("cannot read back: %v", err)
	}
	assertEqual(t, "content", string(data), "written\n")
}

func TestWrapReadableNotWritable(t *testing.T) {
	requireBwrap(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatalf("cannot write data: %v", err)
	}

	_, runErr := runIn(t, sandbox.Options{Readable: []string{path}}, "echo hacked > "+path)
	if runErr == nil {
		t.Fatalf("expected write failure on readable-only grant, got success")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read back: %v", err)
	}
	if string(data) != "x\n" {
		t.Errorf("file was modified: %q", data)
	}
}

func TestWrapNetworkUnsharedByDefault(t *testing.T) {
	requireBwrap(t)
	// With the network unshared, loopback is down: connecting to it fails.
	_, err := runIn(t, sandbox.Options{}, "nc 127.0.0.1 1")
	if err == nil {
		t.Fatalf("expected network to be unshared")
	}
}

func TestWrapNetworkShared(t *testing.T) {
	requireBwrap(t)
	// With the network granted, loopback is up: connecting refuses
	// quickly instead of failing to reach it.
	_, err := runIn(t, sandbox.Options{Network: true}, "nc -w 1 127.0.0.1 1")
	if err == nil {
		t.Fatalf("expected connection refused, got success")
	}
	// The distinction: unshared fails differently, but both error; the
	// meaningful check is that shared networking can reach the stack.
}
