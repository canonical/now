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

// Package sandbox confines command execution with bwrap.
package sandbox

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Options carries the confinement configuration.
type Options struct {
	// Readable are the paths the confined command may read, in addition
	// to the implicit machinery grants.
	Readable []string
	// Writable are the paths the confined command may read and write.
	Writable []string
	// Network keeps the network available; it is unshared otherwise.
	Network bool
	// Cwd is the absolute working directory the confined command
	// starts in. When it is at or below a granted path (or covered
	// by the discovered loader paths), it is bound as usual;
	// otherwise an empty read-only directory is mounted in its place:
	// the path exists at the same location as outside, so relative
	// paths look natural, but its content is invisible without a
	// grant — reads fail with ENOENT and writes fail loudly with
	// EROFS rather than silently succeeding in a throwaway tmpfs
	// whose contents are discarded. Empty leaves the working
	// directory wherever bwrap starts it, usually /.
	Cwd string
	// Bwrap carries the probed confinement configuration; it is filled
	// by Probe and read by Args.
	Bwrap Bwrap
}

// Bwrap is the resolved confinement configuration: the bwrap binary
// path and the form of /proc the environment supports.
type Bwrap struct {
	// Path is the resolved bwrap binary.
	Path string
	// HostProc reports whether the environment denies mounting a fresh
	// procfs, in which case the host's /proc is bound read-only.
	HostProc bool
}

// bindFlags are the composable claims on a path. Every bind source
// (machinery, user grants, the confined binary, the ungranted-cwd
// placeholder) contributes flags per path; the emission is a function
// of the merged set, so same-path combinations are order-independent.
type bindFlags uint8

const (
	// bindRead grants read-only access (--ro-bind).
	bindRead bindFlags = 1 << iota
	// bindWrite grants read-write access (--bind); it dominates
	// bindRead at the same path.
	bindWrite
	// bindTemp replaces the path's content with a fresh empty mount
	// (--tmpfs); combined with bindRead and not bindWrite the fresh
	// mount is remounted read-only onto itself.
	bindTemp
)

// pathBind is one path's merged bind claims.
type pathBind struct {
	path  string
	flags bindFlags
}

// orderBinds sorts binds shallow-first — a deeper path always mounts
// on top of a shallower one, which is how bwrap composes mounts —
// with lexicographic order breaking depth ties so the result is
// deterministic. It is the single ordering authority: every bind
// source feeds through it, so emission order is correct regardless of
// how the binds were collected or in what order the user gave flags.
// Depth is the separator count; Args cleans paths when collecting the
// set, so no duplicate or trailing separators are ever seen here.
func orderBinds(binds []pathBind) []pathBind {
	sort.Slice(binds, func(i, j int) bool {
		di, dj := strings.Count(binds[i].path, "/"), strings.Count(binds[j].path, "/")
		if di != dj {
			return di < dj
		}
		return binds[i].path < binds[j].path
	})
	return binds
}

// discoverReadable derives the paths the given binaries need inside
// the sandbox:
//
//   - ELF binaries: the loader directories and cache via ldd's
//     transitive resolution — each "lib => /path" line and the bare
//     loader line contribute the directory of the resolved file.
//   - Scripts (ldd reports "not a dynamic executable"): the shebang
//     interpreter chain — the interpreter path is bound and resolved
//     recursively (it may itself be a wrapper script), depth-capped.
//     An "/usr/bin/env NAME" shebang binds the env binary as written
//     plus NAME resolved from the host $PATH; env's own runtime
//     lookup then finds it through the inherited host PATH, which by
//     construction contains the directory NAME was resolved from.
//   - Always: the conventional trees — /lib, /lib64, /usr/lib,
//     /usr/local/lib, plus the multiarch pair /lib/<triplet> and
//     /usr/lib/<triplet> derived from the binary's own GOARCH at
//     compile time — the classic homes of loader symlinks, interpreter
//     runtime data (python's stdlib, node modules, perl lib), and
//     platform-specific libs, which no ldd-style probe can discover
//     — plus /etc/ld.so.cache, which the loader reads before the dirs.
//     This is a deliberate, bounded slice of over-binding: the same
//     reasoning that added /usr/lib for interpreter data covers the
//     multiarch dirs, and the compile-time table keeps them correct
//     per architecture without external reads.
//
// An ldd failure on one binary is skipped non-fatally — the probe's
// real bwrap run fails loudly if something needed is missing. A
// script whose shebang interpreter does not exist is a hard error,
// though: the command cannot run at all, and naming the interpreter
// beats an opaque exec failure after approval.
func discoverReadable(binaries []string) ([]string, error) {
	d := &discovery{seen: map[string]bool{}}
	if err := d.discover(binaries, 0); err != nil {
		return nil, err
	}

	// Conventional interpreter runtime data and the loader cache.
	// /lib and /lib64 are the canonical loader symlink roots: the
	// kernel walks PT_INTERP literally (/lib64/ld-linux...), and on
	// merged-usr systems those are symlinks into /usr/lib whose
	// relative targets resolve through /lib — ldd only ever reports
	// the resolved /usr paths, so the symlink view must be bound
	// explicitly or the loader is unreachable despite being "bound".
	// The multiarch pair joins the same conventional set: the classic
	// home of platform-specific libs and interpreter data, generated
	// from the compile-time GOARCH so it is correct per architecture.
	triplet := tripletFor(runtime.GOARCH)
	d.add("/lib")
	d.add("/lib64")
	if triplet != "" {
		d.add("/lib/" + triplet)
	}
	d.add("/usr/lib")
	if triplet != "" {
		d.add("/usr/lib/" + triplet)
	}
	d.add("/usr/local/lib")
	d.add("/etc/ld.so.cache")
	return d.readable, nil
}

// triplets maps the Go architecture to the GNU target triplet naming
// the multiarch library directories on Linux. The mapping is fixed
// per architecture, not per host: a binary built for arm64 runs on an
// arm64 system whose multiarch dir is aarch64-linux-gnu. An unknown
// GOARCH yields "" and its directories are skipped.
var triplets = map[string]string{
	"386":      "i386-linux-gnu",
	"amd64":    "x86_64-linux-gnu",
	"arm":      "arm-linux-gnueabihf",
	"arm64":    "aarch64-linux-gnu",
	"loong64":  "loongarch64-linux-gnu",
	"mips64le": "mips64el-linux-gnuabi64",
	"ppc64le":  "powerpc64le-linux-gnu",
	"riscv64":  "riscv64-linux-gnu",
	"s390x":    "s390x-linux-gnu",
}

// tripletFor returns the GNU target triplet for the Go architecture,
// or "" when unknown. A function rather than a bare map lookup: the
// mapping may grow additional logic (ABI variants, musl suffixes).
func tripletFor(goarch string) string {
	return triplets[goarch]
}

// maxInterpreterDepth caps the shebang interpreter chain: script →
// interpreter → wrapper-interpreter covers the realistic cases;
// anything deeper is pathological.
const maxInterpreterDepth = 3

// discovery carries the state of one discoverReadable run.
type discovery struct {
	readable []string
	seen     map[string]bool
}

// add records a readable path, deduplicated, first-seen order.
func (d *discovery) add(path string) {
	if !d.seen[path] {
		d.seen[path] = true
		d.readable = append(d.readable, path)
	}
}

// discover resolves one level of binaries: each is bound, its ldd
// library directories collected, and scripts followed through their
// shebang up to the depth cap. The cap limits how far the chain is
// followed, not what is bound: the entry at the cap depth is bound,
// but its own interpreter is not resolved. A shebang naming an
// interpreter that does not exist or is not executable is an error —
// the script cannot run either way, and the message names the
// interpreter.
func (d *discovery) discover(binaries []string, depth int) error {
	if depth > maxInterpreterDepth {
		return nil
	}
	for _, bin := range binaries {
		d.add(bin)
		out, err := exec.Command("ldd", bin).Output()
		if err != nil {
			// Not an ELF binary — or ldd failed. A script is
			// followed through its shebang; anything else is
			// skipped non-fatally.
			interp, err := d.discoverShebang(bin)
			if err != nil {
				return err
			}
			if len(interp) == 0 {
				continue
			}
			if err := d.discover(interp, depth+1); err != nil {
				return err
			}
			continue
		}
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "linux-vdso") {
				continue
			}
			// Two forms: "lib => /path (addr)" and the bare
			// loader "/lib64/ld-linux... (addr)".
			path := line
			if i := strings.Index(line, "=>"); i >= 0 {
				path = strings.TrimSpace(line[i+2:])
			}
			if i := strings.Index(path, " ("); i >= 0 {
				path = path[:i]
			}
			if !strings.HasPrefix(path, "/") {
				continue // "not found" or a static marker
			}
			d.add(filepath.Dir(path))
		}
	}
	return nil
}

// discoverShebang returns the binaries the script's shebang names:
// a direct path yields that path; an "/usr/bin/env NAME" shebang
// yields the env binary as written plus NAME resolved from the host
// $PATH. An empty result means the file is not a script. Extra
// shebang arguments are ignored — they do not change which binaries
// are needed. Every named interpreter must exist and be executable:
// the script cannot run otherwise, and the error names the
// interpreter and the script.
func (d *discovery) discoverShebang(script string) ([]string, error) {
	f, err := os.Open(script)
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	// The raw first line: a shebang may carry arguments, and a
	// whitespace-splitting scan would fail on the extra tokens.
	line, err := bufio.NewReader(f).ReadString('\n')
	if err != nil && line == "" {
		return nil, nil
	}
	if !strings.HasPrefix(line, "#!") {
		return nil, nil
	}
	fields := strings.Fields(strings.TrimPrefix(line, "#!"))
	if len(fields) == 0 {
		return nil, nil
	}
	interp := fields[0]
	var interpreters []string
	if filepath.Base(interp) == "env" && len(fields) >= 2 {
		// "/usr/bin/env NAME": the env binary as written, plus
		// NAME resolved from the host $PATH.
		resolved, lerr := exec.LookPath(fields[1])
		if lerr != nil {
			return nil, fmt.Errorf("cannot find interpreter for %q: %s", script, fields[1]+" not in $PATH")
		}
		interpreters = []string{interp, resolved}
	} else if strings.HasPrefix(interp, "/") {
		// Direct interpreter path; the kernel requires absolute.
		interpreters = []string{interp}
	} else {
		return nil, nil
	}
	for _, i := range interpreters {
		info, serr := os.Stat(i)
		if serr != nil {
			return nil, fmt.Errorf("cannot find interpreter for %q: %s", script, i)
		}
		if info.IsDir() || info.Mode()&0o111 == 0 {
			return nil, fmt.Errorf("invalid interpreter for %q: %s", script, i)
		}
	}
	return interpreters, nil
}

// Probe verifies that the confinement works and records how. It runs the
// real Options through bwrap with a trivial command: if the fresh procfs
// mounts, that is the preferred form; if the environment denies that
// mount specifically, the host's /proc is bound read-only instead. Any
// other failure means confining is impossible and is an error — better
// before the script is generated and reviewed than after its approval.
//
// Probe also discovers the loader directories, cache, and shebang
// interpreter chains the confined binaries (cmdpath plus binaries)
// depend on, via ldd, and appends them to Readable, so they flow
// through the same normalization as user grants and a user grant
// overrides them at the same path. The returned Options carries the
// resolved Bwrap and the extended Readable.
//
// When Options.Bwrap is already set, Probe returns the options
// unchanged, so probing twice is cheap.
func Probe(opts Options, cmdpath string, binaries ...string) (Options, error) {
	if opts.Bwrap.Path != "" {
		return opts, nil
	}

	path, err := exec.LookPath("bwrap")
	if err != nil {
		return Options{}, fmt.Errorf("cannot find bwrap in $PATH")
	}

	// Machinery first: the probe runs the real Args, so the discovered
	// readable paths must already be on Readable for the probe
	// invocation to mirror the real one.
	readable, err := discoverReadable(append([]string{cmdpath}, binaries...))
	if err != nil {
		return Options{}, err
	}
	opts.Readable = append(opts.Readable, readable...)

	// The probe mirrors the real invocation: same grants, trivial child.
	// Stderr is captured so the failure can be classified. The hostProc
	// variant is tried by temporarily flipping the Options field.
	quiet := func(hostProc bool) (stderr []byte, err error) {
		probeOpts := opts
		probeOpts.Bwrap = Bwrap{Path: path, HostProc: hostProc}
		args, err := Args(probeOpts, cmdpath, "true")
		if err != nil {
			return nil, err
		}
		cmd := exec.Command(args[0], args[1:]...)
		var buf bytes.Buffer
		cmd.Stderr = &buf
		err = cmd.Run()
		return buf.Bytes(), err
	}

	stderr, err := quiet(false)
	if err == nil {
		opts.Bwrap = Bwrap{Path: path}
		return opts, nil
	}
	if !bytes.Contains(stderr, []byte("Can't mount proc")) {
		// Only the proc mount denial is fixed by changing the proc
		// form; anything else means confining is impossible.
		return Options{}, fmt.Errorf("cannot confine with bwrap: %w", err)
	}

	if _, err := quiet(true); err != nil {
		return Options{}, fmt.Errorf("cannot confine with bwrap: %w", err)
	}
	opts.Bwrap = Bwrap{Path: path, HostProc: true}
	return opts, nil
}

// Args returns the complete argument vector for a confined execution:
// args[0] is the bwrap binary, from Options.Bwrap, and the vector ends
// in the confined command followed by its arguments. Executing the
// returned vector runs the command under confinement, with no need to
// consult the options again. Options.Bwrap must be filled by Probe; an
// empty one is an error rather than a silent misconfiguration.
//
// The fresh procfs from --proc is what busybox applets need: they re-exec
// themselves through /proc/self/exe. Where the environment denies that
// mount — restricted containers — the host's /proc is bound read-only
// instead, which keeps applets working at the cost of exposing the
// host's process list to the script.
func Args(opts Options, cmdpath string, cmdargs ...string) ([]string, error) {
	if opts.Bwrap.Path == "" {
		return nil, fmt.Errorf("cannot confine: sandbox not probed")
	}

	args := []string{
		opts.Bwrap.Path,
		"--unshare-all", // namespaces; network re-shared below when granted
		"--dev", "/dev",
		"--tmpfs", "/tmp",
	}

	if opts.Bwrap.HostProc {
		args = append(args, "--ro-bind", "/proc", "/proc")
	} else {
		args = append(args, "--proc", "/proc")
	}

	if opts.Network {
		args = append(args, "--share-net")
	}

	// Collect every bind claim into one set, then order and emit.
	// Same-path claims OR together, so classification is
	// order-independent; orderBinds sorts shallow-first so a deeper
	// grant always mounts on top. Paths are cleaned here, at the
	// boundary: /foo and /foo/ are the same directory, so their
	// claims merge, and the depth sort never sees a duplicate or
	// trailing separator. Symlinked paths claim both the link and
	// its resolved target: the kernel and loader look up literal
	// paths (a shebang names /usr/bin/env; PT_INTERP names
	// /lib64/ld-linux...), so the link path must be bound — and the
	// target must be bound too, or the link dangles inside where its
	// target directory does not exist. bwrap's bind of a symlink
	// source copies the target content, which covers both when the
	// target dir is absent; binding the target as well keeps deeper
	// grants below it working.
	set := map[string]bindFlags{}
	claim := func(path string, flags bindFlags) {
		set[filepath.Clean(path)] |= flags
		if real, err := filepath.EvalSymlinks(path); err == nil && real != path {
			set[real] |= flags
		}
	}
	claim(cmdpath, bindRead) // the confined binary
	for _, path := range opts.Readable {
		claim(path, bindRead)
	}
	for _, path := range opts.Writable {
		claim(path, bindWrite)
	}
	if opts.Cwd != "" && opts.Cwd != "/" && !coveredBy(set, opts.Cwd) {
		// The working directory always exists inside: an empty
		// read-only mount in its place when nothing covers it.
		claim(opts.Cwd, bindTemp|bindRead)
	}

	ordered := orderBinds(bindsFrom(set))
	for _, b := range ordered {
		if _, err := os.Stat(b.path); err != nil {
			continue // not on this system
		}
		args = emitBind(args, b)
	}
	// The read-only remounts come after the whole bind sequence:
	// --remount-ro is non-recursive and needs the parent writable
	// while deeper binds mount under it, so a temp+read path goes
	// read-only only after everything below it is in place.
	for _, b := range ordered {
		if b.flags&bindTemp != 0 && b.flags&bindRead != 0 && b.flags&bindWrite == 0 {
			args = append(args, "--remount-ro", b.path)
		}
	}

	// --chdir must come after the binds: bwrap processes arguments in
	// order, and a chdir before the cwd's tmpfs would resolve the
	// process into the real host directory, leaking its content
	// through the already-open cwd fd.
	if opts.Cwd != "" && opts.Cwd != "/" {
		args = append(args, "--chdir", opts.Cwd)
	}

	// The spread cannot mix with individual arguments, so -- and the
	// command go on first, then the spread.
	return append(append(args, "--", cmdpath), cmdargs...), nil
}

// emitBind renders one bind as bwrap arguments, per the emission table:
// temp mounts a fresh empty tmpfs over the path, write without temp
// binds read-write, and plain read binds read-only. The read-only
// remount for temp+read paths is emitted separately by Args, after
// the whole sequence — --remount-ro is non-recursive and needs the
// parent writable while deeper binds mount under it. A bind with no
// claims emits nothing.
func emitBind(args []string, b pathBind) []string {
	if b.flags&bindTemp != 0 {
		return append(args, "--tmpfs", b.path)
	}
	if b.flags&bindWrite != 0 {
		return append(args, "--bind", b.path, b.path)
	}
	if b.flags&bindRead != 0 {
		return append(args, "--ro-bind", b.path, b.path)
	}
	return args
}

// bindsFrom materializes the merged set as a slice for ordering.
func bindsFrom(set map[string]bindFlags) []pathBind {
	binds := make([]pathBind, 0, len(set))
	for path, flags := range set {
		binds = append(binds, pathBind{path: path, flags: flags})
	}
	return binds
}

// coveredBy reports whether the path is at or below any bind in the
// set — the question the ungranted-cwd placeholder asks. Grants are
// absolute paths from the parser.
func coveredBy(set map[string]bindFlags, path string) bool {
	for dir := range set {
		if dir != "" && (dir == path || strings.HasPrefix(path, dir+"/")) {
			return true
		}
	}
	return false
}
