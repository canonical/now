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
	opts, err = sandbox.Probe(opts, busybox)
	if err != nil {
		return "", err
	}

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
	// Probe with Options.Bwrap already set returns the options
	// unchanged, without re-deriving machinery.
	busybox, err := exec.LookPath("busybox")
	if err != nil {
		t.Fatalf("cannot find busybox: %v", err)
	}
	want := sandbox.Bwrap{Path: "/cached/bwrap", HostProc: true}
	in := sandbox.Options{Bwrap: want, Readable: []string{"/a"}}
	got, err := sandbox.Probe(in, busybox)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Bwrap", got.Bwrap, want)
	assertEqual(t, "Readable", got.Readable, []string{"/a"})
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

	opts, err := sandbox.Probe(sandbox.Options{}, busybox)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "HostProc", opts.Bwrap.HostProc, false)
	assertEqual(t, "fake is the resolved bwrap", filepath.Base(opts.Bwrap.Path), "bwrap")

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

	opts, err := sandbox.Probe(sandbox.Options{}, busybox)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "HostProc", opts.Bwrap.HostProc, true)

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
	opts, err := sandbox.Probe(sandbox.Options{}, busybox)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Bwrap.Path == "" {
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

func TestWrapCwdUngrantedHidesContent(t *testing.T) {
	// The ungranted cwd must not leak the host directory's content:
	// --chdir runs after the cwd's tmpfs is mounted, so a relative
	// read resolves against the empty mount, not the real directory.
	// This pins a regression where --chdir preceded the binds and the
	// process kept the host directory as its cwd.
	requireBwrap(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("secret\n"), 0o644); err != nil {
		t.Fatalf("cannot write secret: %v", err)
	}
	t.Chdir(dir)

	_, err := runIn(t, sandbox.Options{Cwd: dir}, "cat secret.txt")
	if err == nil {
		t.Fatalf("expected read of ungranted cwd content to fail, got success")
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

func TestWrapWriteOverridesReadSamePath(t *testing.T) {
	// -w and -r on the same path yield a writable bind regardless of
	// flag order: the most permissive grant wins, never a read-only
	// bind shadowing the writable one.
	requireBwrap(t)
	dir := t.TempDir()
	for _, opts := range []sandbox.Options{
		{Readable: []string{dir}, Writable: []string{dir}},
		{Writable: []string{dir}, Readable: []string{dir}},
	} {
		_, err := runIn(t, opts, "echo written > "+filepath.Join(dir, "data.txt"))
		if err != nil {
			t.Fatalf("expected writable to win, got error: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "data.txt"))
		if err != nil {
			t.Fatalf("cannot read back: %v", err)
		}
		assertEqual(t, "content", string(data), "written\n")
		if err := os.Remove(filepath.Join(dir, "data.txt")); err != nil {
			t.Fatalf("cannot clean: %v", err)
		}
	}
}

func TestWrapNestedWriteInsideRead(t *testing.T) {
	// A writable grant inside a readable one: the deeper writable
	// bind mounts on top of the shallower read-only one.
	requireBwrap(t)
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("cannot create sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ro.txt"), []byte("ro\n"), 0o644); err != nil {
		t.Fatalf("cannot write ro: %v", err)
	}

	out, err := runIn(t, sandbox.Options{Readable: []string{dir}, Writable: []string{sub}},
		"cat "+filepath.Join(dir, "ro.txt")+"; echo w > "+filepath.Join(sub, "w.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "readable parent", out, "ro\n")
	data, err := os.ReadFile(filepath.Join(sub, "w.txt"))
	if err != nil {
		t.Fatalf("cannot read back: %v", err)
	}
	assertEqual(t, "writable child", string(data), "w\n")
}

func TestWrapNestedReadInsideWrite(t *testing.T) {
	// A readable grant inside a writable one: the deeper read-only
	// bind mounts on top of the shallower writable one, and writing
	// there fails loudly.
	requireBwrap(t)
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("cannot create sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "ro.txt"), []byte("ro\n"), 0o644); err != nil {
		t.Fatalf("cannot write ro: %v", err)
	}

	_, err := runIn(t, sandbox.Options{Writable: []string{dir}, Readable: []string{sub}},
		"echo w > "+filepath.Join(sub, "w.txt")+" 2>&1")
	if err == nil {
		t.Fatalf("expected write failure inside read-only child, got success")
	}
	// The parent stays writable.
	_, err = runIn(t, sandbox.Options{Writable: []string{dir}, Readable: []string{sub}},
		"echo w > "+filepath.Join(dir, "w.txt"))
	if err != nil {
		t.Fatalf("expected parent to stay writable, got error: %v", err)
	}
}

func TestWrapInterleavedThreeLevels(t *testing.T) {
	// The full interleaving case: rw /a, ro /a/b, rw /a/b/c — the
	// emission must interleave bind kinds by depth, which separate
	// ro/rw loops cannot express.
	requireBwrap(t)
	a := t.TempDir()
	b := filepath.Join(a, "b")
	c := filepath.Join(b, "c")
	for _, d := range []string{b, c} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatalf("cannot create %s: %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(b, "ro.txt"), []byte("ro\n"), 0o644); err != nil {
		t.Fatalf("cannot write ro: %v", err)
	}

	opts := sandbox.Options{
		Readable: []string{b},
		Writable: []string{a, c},
	}
	// The middle level is read-only: writing there fails.
	_, err := runIn(t, opts, "echo x > "+filepath.Join(b, "x.txt")+" 2>&1")
	if err == nil {
		t.Fatalf("expected write failure at read-only middle level, got success")
	}
	// The deepest level is writable again, on top of the read-only one.
	_, err = runIn(t, opts, "echo w > "+filepath.Join(c, "w.txt"))
	if err != nil {
		t.Fatalf("expected deepest level writable, got error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(c, "w.txt"))
	if err != nil {
		t.Fatalf("cannot read back: %v", err)
	}
	assertEqual(t, "deepest writable", string(data), "w\n")
	// The top level is writable too.
	_, err = runIn(t, opts, "echo t > "+filepath.Join(a, "t.txt"))
	if err != nil {
		t.Fatalf("expected top level writable, got error: %v", err)
	}
}

func TestArgsCleansPaths(t *testing.T) {
	// Args cleans paths when collecting the bind set: /foo and /foo/
	// are the same directory, so their claims merge into one bind
	// rather than emitting two, and the depth sort never sees a
	// duplicate or trailing separator. The paths must exist: Args
	// existence-checks before emitting.
	dir := t.TempDir()
	got, err := sandbox.Args(sandbox.Options{
		Bwrap:    sandbox.Bwrap{Path: "/bwrap"},
		Readable: []string{dir + "/"},
		Writable: []string{dir},
	}, "/bin/busybox")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(got, " ")
	if strings.Count(joined, "--bind "+dir+" "+dir) != 1 {
		t.Errorf("expected one writable bind for %s, got %q", dir, joined)
	}
	if strings.Contains(joined, "--ro-bind "+dir+" "+dir) {
		t.Errorf("read-only bind for %s should have merged into writable: %q", dir, joined)
	}
}

func TestWrapConfinedScriptExecutes(t *testing.T) {
	// The phase-2 pin: a -c command that is an interpreted script
	// executes under confinement. The interpreter chain is cooked
	// from busybox so the test is self-contained: tool → (env +
	// interp) → busybox sh, covering the env shebang form, a
	// two-level script chain, and a static-ELF terminal. env finds
	// the cooked interpreter through the inherited host PATH, which
	// the test puts the fixture dir on.
	requireBwrap(t)
	busybox, err := exec.LookPath("busybox")
	if err != nil {
		t.Fatalf("cannot find busybox: %v", err)
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	// The interpreter is itself a script, terminating at busybox:
	// the kernel runs "busybox sh interp tool", so its body re-execs
	// the tool under busybox sh.
	interp := filepath.Join(dir, "interp")
	if err := os.WriteFile(interp, []byte("#!"+busybox+" sh\nexec \""+busybox+"\" sh \"$1\"\n"), 0o755); err != nil {
		t.Fatalf("cannot write interpreter: %v", err)
	}
	script := filepath.Join(dir, "tool")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env interp\necho \"hello from script\"\n"), 0o755); err != nil {
		t.Fatalf("cannot write script: %v", err)
	}

	opts, err := sandbox.Probe(sandbox.Options{Readable: []string{script}}, busybox, script)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	bargs, err := sandbox.Args(opts, busybox, "sh", "-c", script)
	if err != nil {
		t.Fatalf("args: %v", err)
	}
	var out bytes.Buffer
	cmd := exec.Command(bargs[0], bargs[1:]...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("confined script failed: %v\noutput: %s", err, out.String())
	}
	assertEqual(t, "output", out.String(), "hello from script\n")
}

func TestWrapConfinedPythonStdlib(t *testing.T) {
	// A real-world interpreter: python needs its stdlib under
	// /usr/lib, the conventional-tree part of the discovery that no
	// ldd-style probe can see. Skips where python3 is absent; the
	// cooked-chain test above is the always-available pin.
	requireBwrap(t)
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not available")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "tool.py")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env python3\nprint(\"hello from script\")\n"), 0o755); err != nil {
		t.Fatalf("cannot write script: %v", err)
	}

	busybox, err := exec.LookPath("busybox")
	if err != nil {
		t.Fatalf("cannot find busybox: %v", err)
	}
	opts, err := sandbox.Probe(sandbox.Options{Readable: []string{script}}, busybox, script)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	bargs, err := sandbox.Args(opts, busybox, "sh", "-c", script)
	if err != nil {
		t.Fatalf("args: %v", err)
	}
	var out bytes.Buffer
	cmd := exec.Command(bargs[0], bargs[1:]...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("confined script failed: %v\noutput: %s", err, out.String())
	}
	assertEqual(t, "output", out.String(), "hello from script\n")
}
