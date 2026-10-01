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

// Internal tests for the bind ordering and machinery derivation, which
// are unexported; the black-box suite lives in sandbox_test.go.
package sandbox

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func assertEqual[T any](t *testing.T, label string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", label, got, want)
	}
}

// mustDiscover runs discoverReadable, failing the test on error.
func mustDiscover(t *testing.T, binaries []string) []string {
	t.Helper()
	got, err := discoverReadable(binaries)
	if err != nil {
		t.Fatalf("discoverReadable(%q) unexpected error: %v", binaries, err)
	}
	return got
}

func TestOrderBindsShallowFirst(t *testing.T) {
	// Deeper paths mount on top of shallower ones; ties break
	// lexicographically so the result is deterministic.
	got := orderBinds([]pathBind{
		{path: "/a/b/c", flags: bindRead},
		{path: "/a", flags: bindRead},
		{path: "/a/b", flags: bindWrite},
		{path: "/z", flags: bindRead},
	})
	want := []pathBind{
		{path: "/a", flags: bindRead},
		{path: "/z", flags: bindRead},
		{path: "/a/b", flags: bindWrite},
		{path: "/a/b/c", flags: bindRead},
	}
	assertEqual(t, "orderBinds", got, want)
}

func TestOrderBindsPermutationDeterministic(t *testing.T) {
	// Any input permutation yields the same ordered output.
	base := []pathBind{
		{path: "/w/x", flags: bindRead},
		{path: "/w", flags: bindWrite},
		{path: "/w/x/y", flags: bindRead},
		{path: "/q", flags: bindRead},
	}
	want := orderBinds(append([]pathBind(nil), base...))
	perms := [][]pathBind{
		{base[3], base[2], base[1], base[0]},
		{base[2], base[0], base[3], base[1]},
		{base[1], base[3], base[0], base[2]},
	}
	for i, p := range perms {
		assertEqual(t, "permutation "+string(rune('0'+i)), orderBinds(p), want)
	}
}

func TestMergeBindsOrderIndependent(t *testing.T) {
	// Same-path claims OR together in any order: the emission table
	// is a function of the merged set alone.
	table := []struct {
		name  string
		flags []bindFlags
		want  bindFlags
	}{
		{"read", []bindFlags{bindRead}, bindRead},
		{"write", []bindFlags{bindWrite}, bindWrite},
		{"temp", []bindFlags{bindTemp}, bindTemp},
		{"read+temp", []bindFlags{bindRead, bindTemp}, bindRead | bindTemp},
		{"write+temp", []bindFlags{bindWrite, bindTemp}, bindWrite | bindTemp},
		{"read+write", []bindFlags{bindRead, bindWrite}, bindRead | bindWrite},
		{"read+write+temp", []bindFlags{bindRead, bindWrite, bindTemp}, bindRead | bindWrite | bindTemp},
	}
	for _, tt := range table {
		t.Run(tt.name, func(t *testing.T) {
			// Both input orders must merge identically.
			var a, b map[string]bindFlags = map[string]bindFlags{}, map[string]bindFlags{}
			for _, f := range tt.flags {
				a["/p"] |= f
			}
			for i := len(tt.flags) - 1; i >= 0; i-- {
				b["/p"] |= tt.flags[i]
			}
			assertEqual(t, "forward", a["/p"], tt.want)
			assertEqual(t, "reverse", b["/p"], tt.want)
		})
	}
}

func TestCoveredBy(t *testing.T) {
	set := map[string]bindFlags{
		"/a/b":  bindRead,
		"/c":    bindWrite,
		"/d/e":  bindTemp | bindRead,
	}
	assertEqual(t, "exact", coveredBy(set, "/a/b"), true)
	assertEqual(t, "below", coveredBy(set, "/a/b/c"), true)
	assertEqual(t, "above", coveredBy(set, "/a"), false)
	assertEqual(t, "unrelated", coveredBy(set, "/q"), false)
	assertEqual(t, "sibling prefix", coveredBy(set, "/a/bb"), false)
}

func TestTripletFor(t *testing.T) {
	// The GOARCH→triplet mapping is fixed per architecture; unknown
	// architectures yield "" and their directories are skipped.
	table := []struct {
		goarch string
		want   string
	}{
		{"386", "i386-linux-gnu"},
		{"amd64", "x86_64-linux-gnu"},
		{"arm", "arm-linux-gnueabihf"},
		{"arm64", "aarch64-linux-gnu"},
		{"loong64", "loongarch64-linux-gnu"},
		{"mips64le", "mips64el-linux-gnuabi64"},
		{"ppc64le", "powerpc64le-linux-gnu"},
		{"riscv64", "riscv64-linux-gnu"},
		{"s390x", "s390x-linux-gnu"},
		{"wasm", ""},
	}
	for _, tt := range table {
		assertEqual(t, "tripletFor("+tt.goarch+")", tripletFor(tt.goarch), tt.want)
	}
}

func TestDiscoverReadableInjectsClassics(t *testing.T) {
	// The conventional set must actually include the classic library
	// directories — the injection, not just the map. A static binary
	// keeps the output to the conventional set alone, so the dirs are
	// directly observable.
	fakeLdd(t, t.TempDir(), map[string]string{
		"busybox": "\tnot a dynamic executable\n",
	})
	got := mustDiscover(t, []string{"/bin/busybox"})
	triplet := tripletFor(runtime.GOARCH)
	if triplet == "" {
		t.Skipf("no triplet for GOARCH %q", runtime.GOARCH)
	}
	var dirs []string
	for _, dir := range []string{"/lib", "/lib64", "/usr/lib", "/usr/local/lib", "/etc/ld.so.cache"} {
		dirs = append(dirs, dir)
	}
	dirs = append(dirs, "/lib/"+triplet, "/usr/lib/"+triplet)
	for _, dir := range dirs {
		found := false
		for _, p := range got {
			if p == dir {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("classic dir %q missing from discovery: %v", dir, got)
		}
	}
}

// fakeLdd installs a fake ldd on $PATH whose behavior is keyed by the
// basename of the binary it is invoked with: the script cats a reply
// file named after the basename (exit 0), or exits 1 when none exists.
func fakeLdd(t *testing.T, dir string, replies map[string]string) {
	t.Helper()
	for name, out := range replies {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(out), 0o644); err != nil {
			t.Fatalf("cannot write reply: %v", err)
		}
	}
	script := "#!/bin/sh\ndir=$(dirname \"$0\")\nbase=$(basename \"$1\")\nif [ -f \"$dir/$base\" ]; then cat \"$dir/$base\"; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "ldd"), []byte(script), 0o755); err != nil {
		t.Fatalf("cannot write fake ldd: %v", err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

func TestDiscoverReadableDynamic(t *testing.T) {
	// Resolved lib lines and the bare loader line both contribute
	// their directories; the conventional trees and cache are always
	// included.
	fakeLdd(t, t.TempDir(), map[string]string{
		"curl": "\tlinux-vdso.so.1 (0x7f00)\n" +
			"\tlibssl.so.3 => /usr/lib/x86_64-linux-gnu/libssl.so.3 (0x7f01)\n" +
			"\tlibc.so.6 => /lib/x86_64-linux-gnu/libc.so.6 (0x7f02)\n" +
			"\t/lib64/ld-linux-x86-64.so.2 (0x7f03)\n",
	})
	got := mustDiscover(t, []string{"/bin/curl"})
	want := []string{
		"/bin/curl",
		"/usr/lib/x86_64-linux-gnu",
		"/lib/x86_64-linux-gnu",
		"/lib64",
		"/lib",
		"/usr/lib",
		"/usr/local/lib",
		"/etc/ld.so.cache",
	}
	assertEqual(t, "readable", got, want)
}

func TestDiscoverReadableStatic(t *testing.T) {
	// A static binary contributes itself plus the conventional trees
	// and the cache; no library directories.
	fakeLdd(t, t.TempDir(), map[string]string{
		"busybox": "\tnot a dynamic executable\n",
	})
	got := mustDiscover(t, []string{"/bin/busybox"})
	assertEqual(t, "readable", got, []string{"/bin/busybox", "/lib", "/lib64", "/lib/x86_64-linux-gnu", "/usr/lib", "/usr/lib/x86_64-linux-gnu", "/usr/local/lib", "/etc/ld.so.cache"})
}

func TestDiscoverReadableLddFailureSkipped(t *testing.T) {
	// An ldd failure on one binary is non-fatal: that binary
	// contributes only itself, others still resolve.
	fakeLdd(t, t.TempDir(), map[string]string{
		"works": "\tlibc.so.6 => /lib/x86_64-linux-gnu/libc.so.6 (0x7f00)\n",
	})
	got := mustDiscover(t, []string{"/bin/broken", "/bin/works"})
	assertEqual(t, "readable", got, []string{"/bin/broken", "/bin/works", "/lib/x86_64-linux-gnu", "/lib", "/lib64", "/usr/lib", "/usr/lib/x86_64-linux-gnu", "/usr/local/lib", "/etc/ld.so.cache"})
}

func TestDiscoverReadableMultipleBinaries(t *testing.T) {
	// Multiple binaries contribute the union of their directories,
	// deduplicated.
	fakeLdd(t, t.TempDir(), map[string]string{
		"one": "\tliba.so => /usr/lib/a/liba.so (0x1)\n",
		"two": "\tliba.so => /usr/lib/a/liba.so (0x1)\n\tlibb.so => /usr/lib/b/libb.so (0x2)\n",
	})
	got := mustDiscover(t, []string{"/bin/one", "/bin/two"})
	assertEqual(t, "readable", got, []string{"/bin/one", "/usr/lib/a", "/bin/two", "/usr/lib/b", "/lib", "/lib64", "/lib/x86_64-linux-gnu", "/usr/lib", "/usr/lib/x86_64-linux-gnu", "/usr/local/lib", "/etc/ld.so.cache"})
}

func TestDiscoverReadableNotFound(t *testing.T) {
	// A "lib => not found" line carries no path and is skipped.
	fakeLdd(t, t.TempDir(), map[string]string{
		"x": "\tlibmissing.so.1 => not found\n",
	})
	got := mustDiscover(t, []string{"/bin/x"})
	assertEqual(t, "readable", got, []string{"/bin/x", "/lib", "/lib64", "/lib/x86_64-linux-gnu", "/usr/lib", "/usr/lib/x86_64-linux-gnu", "/usr/local/lib", "/etc/ld.so.cache"})
}

// writeScript creates an executable fixture with the given shebang line
// (including the #!) and body, returning its path.
func writeScript(t *testing.T, dir, name, shebang, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := shebang + "\n" + body
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("cannot write script: %v", err)
	}
	return path
}

func TestDiscoverReadableDirectShebang(t *testing.T) {
	// A script with a direct interpreter path: the interpreter is
	// bound and its ldd libraries discovered; no PATH env needed.
	// The fake ldd's reply dir is separate from the fixtures, so the
	// fixture scripts are not mistaken for reply files. The
	// interpreter is a real executable file: a missing one is now a
	// hard error.
	fakeLdd(t, t.TempDir(), map[string]string{
		"interp": "\tlibc.so.6 => /lib/x86_64-linux-gnu/libc.so.6 (0x7f00)\n",
	})
	dir := t.TempDir()
	interp := writeScript(t, dir, "interp", "#!/bin/sh", "exit 0")
	script := writeScript(t, dir, "myscript", "#!"+interp, "echo hi")
	got := mustDiscover(t, []string{script})
	assertEqual(t, "readable", got, []string{
		script,
		interp,
		"/lib/x86_64-linux-gnu",
		"/lib",
		"/lib64",
		"/usr/lib",
		"/usr/lib/x86_64-linux-gnu",
		"/usr/local/lib",
		"/etc/ld.so.cache",
	})
}

func TestDiscoverReadableEnvShebang(t *testing.T) {
	// An /usr/bin/env NAME shebang: the env binary as written, plus
	// NAME resolved from the host $PATH, plus its directory in the
	// sandbox PATH so env's runtime lookup finds it.
	fakeLdd(t, t.TempDir(), map[string]string{
		"env":     "\tlibc.so.6 => /lib/x86_64-linux-gnu/libc.so.6 (0x1)\n",
		"mytool": "\tlibt.so => /usr/lib/t/libt.so (0x2)\n",
	})
	dir := t.TempDir()
	// A fake mytool on $PATH for the LookPath resolution.
	toolDir := t.TempDir()
	toolPath := filepath.Join(toolDir, "mytool")
	if err := os.WriteFile(toolPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("cannot write tool: %v", err)
	}
	t.Setenv("PATH", toolDir+":"+os.Getenv("PATH"))

	script := writeScript(t, dir, "myscript", "#!/usr/bin/env mytool", "echo hi")
	got := mustDiscover(t, []string{script})
	assertEqual(t, "readable", got, []string{
		script,
		"/usr/bin/env",
		"/lib/x86_64-linux-gnu",
		toolPath,
		"/usr/lib/t",
		"/lib",
		"/lib64",
		"/usr/lib",
		"/usr/lib/x86_64-linux-gnu",
		"/usr/local/lib",
		"/etc/ld.so.cache",
	})
}

func TestDiscoverReadableEnvShebangUnresolvedName(t *testing.T) {
	// An env shebang naming a program not in $PATH is a hard error:
	// env cannot find the program, so the script cannot run. The
	// message names the missing program.
	fakeLdd(t, t.TempDir(), map[string]string{
		"env": "\tlibc.so.6 => /lib/x86_64-linux-gnu/libc.so.6 (0x1)\n",
	})

	dir := t.TempDir()
	script := writeScript(t, dir, "myscript", "#!/usr/bin/env now-absent-tool-xyz", "echo hi")
	_, err := discoverReadable([]string{script})
	if err == nil || !strings.Contains(err.Error(), `cannot find interpreter for `) || !strings.Contains(err.Error(), "not in $PATH") {
		t.Fatalf("expected missing env name error, got %v", err)
	}
}

func TestDiscoverReadableWrapperChain(t *testing.T) {
	// The interpreter may itself be a script: the chain recurses,
	// depth-capped. The interpreters are real executable files.
	fakeLdd(t, t.TempDir(), map[string]string{
		"realinterp": "\tlibc.so.6 => /lib/x86_64-linux-gnu/libc.so.6 (0x1)\n",
	})
	dir := t.TempDir()
	realinterp := writeScript(t, dir, "realinterp", "#!/bin/sh", "exit 0")
	wrapper := writeScript(t, dir, "wrapper", "#!"+realinterp, "exec realinterp")
	script := writeScript(t, dir, "myscript", "#!"+wrapper, "echo hi")
	got := mustDiscover(t, []string{script})
	assertEqual(t, "readable", got, []string{
		script,
		wrapper,
		realinterp,
		"/lib/x86_64-linux-gnu",
		"/lib",
		"/lib64",
		"/usr/lib",
		"/usr/lib/x86_64-linux-gnu",
		"/usr/local/lib",
		"/etc/ld.so.cache",
	})
}

func TestDiscoverReadableDepthCap(t *testing.T) {
	// A chain deeper than maxInterpreterDepth stops recursing: the
	// deepest wrapper is bound but its interpreter is not followed.
	fakeLdd(t, t.TempDir(), map[string]string{})
	dir := t.TempDir()
	// Build a chain: s0 -> s1 -> s2 -> s3 -> s4 (each a script).
	s4 := writeScript(t, dir, "s4", "#!/bin/deep4", "x")
	s3 := writeScript(t, dir, "s3", "#!"+s4, "x")
	s2 := writeScript(t, dir, "s2", "#!"+s3, "x")
	s1 := writeScript(t, dir, "s1", "#!"+s2, "x")
	s0 := writeScript(t, dir, "s0", "#!"+s1, "x")
	got := mustDiscover(t, []string{s0})
	// Depth 0: s0; 1: s1; 2: s2 — the cap stops before s3's interpreter.
	assertEqual(t, "readable", got, []string{
		s0, s1, s2, s3,
		"/lib", "/lib64", "/lib/x86_64-linux-gnu", "/usr/lib", "/usr/lib/x86_64-linux-gnu", "/usr/local/lib", "/etc/ld.so.cache",
	})
}

func TestDiscoverReadableNonScriptSkipped(t *testing.T) {
	// A file that is neither ELF nor script (no shebang) contributes
	// only itself; nothing is followed.
	fakeLdd(t, t.TempDir(), map[string]string{})
	dir := t.TempDir()
	data := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(data, []byte("just text\n"), 0o644); err != nil {
		t.Fatalf("cannot write data: %v", err)
	}
	got := mustDiscover(t, []string{data})
	assertEqual(t, "readable", got, []string{data, "/lib", "/lib64", "/lib/x86_64-linux-gnu", "/usr/lib", "/usr/lib/x86_64-linux-gnu", "/usr/local/lib", "/etc/ld.so.cache"})
}

func TestDiscoverReadableMissingInterpreter(t *testing.T) {
	// A script whose shebang names an interpreter that does not exist
	// is a hard error naming both: the command cannot run at all,
	// and the message beats an opaque exec failure after approval.
	fakeLdd(t, t.TempDir(), map[string]string{})
	dir := t.TempDir()
	script := writeScript(t, dir, "myscript", "#!/bin/nosuchinterp", "echo hi")
	_, err := discoverReadable([]string{script})
	if err == nil || !strings.Contains(err.Error(), `cannot find interpreter for `) || !strings.Contains(err.Error(), "/bin/nosuchinterp") {
		t.Fatalf("expected missing interpreter error, got %v", err)
	}
}

func TestDiscoverReadableNonExecutableInterpreter(t *testing.T) {
	// A shebang naming an existing but non-executable interpreter is
	// the same hard error class: exec would fail with EACCES, so the
	// probe names the interpreter instead.
	fakeLdd(t, t.TempDir(), map[string]string{})
	dir := t.TempDir()
	interp := filepath.Join(dir, "interp")
	if err := os.WriteFile(interp, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil { // no execute bit
		t.Fatalf("cannot write interpreter: %v", err)
	}
	script := writeScript(t, dir, "myscript", "#!"+interp, "echo hi")
	_, err := discoverReadable([]string{script})
	if err == nil || !strings.Contains(err.Error(), `invalid interpreter for `) {
		t.Fatalf("expected non-executable interpreter error, got %v", err)
	}
}

func TestDiscoverReadableMissingEnvName(t *testing.T) {
	// An env shebang whose NAME does not resolve is the same hard
	// error: env itself exists, but the program it must find does
	// not, so the script cannot run.
	fakeLdd(t, t.TempDir(), map[string]string{})
	dir := t.TempDir()
	script := writeScript(t, dir, "myscript", "#!/usr/bin/env now-absent-tool-xyz", "echo hi")
	_, err := discoverReadable([]string{script})
	if err == nil || !strings.Contains(err.Error(), `cannot find interpreter `) {
		t.Fatalf("expected missing interpreter error, got %v", err)
	}
}

func TestArgsEmissionTable(t *testing.T) {
	// The emission is a function of the merged flag set: temp mounts
	// a fresh tmpfs, read without write remounts it read-only, write
	// without temp binds read-write.
	table := []struct {
		name  string
		flags bindFlags
		want  []string
	}{
		{"read", bindRead, []string{"--ro-bind", "/p", "/p"}},
		{"write", bindWrite, []string{"--bind", "/p", "/p"}},
		{"temp", bindTemp, []string{"--tmpfs", "/p"}},
		{"read+temp", bindRead | bindTemp, []string{"--tmpfs", "/p"}},
		{"write+temp", bindWrite | bindTemp, []string{"--tmpfs", "/p"}},
		{"read+write", bindRead | bindWrite, []string{"--bind", "/p", "/p"}},
		{"read+write+temp", bindRead | bindWrite | bindTemp, []string{"--tmpfs", "/p"}},
		{"none", 0, nil},
	}
	for _, tt := range table {
		t.Run(tt.name, func(t *testing.T) {
			set := map[string]bindFlags{"/p": tt.flags}
			var args []string
			for _, b := range orderBinds(bindsFrom(set)) {
				args = emitBind(args, b)
			}
			assertEqual(t, "emission", args, tt.want)
		})
	}
}
