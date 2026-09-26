// Package sandbox confines command execution with bwrap.
package sandbox

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
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

// implicit are the paths confinement needs to work at all: its libraries
// and the loader cache. They carry no user data. Devices come from --dev,
// and procfs from the probe-resolved form.
var implicit = []string{
	"/etc/ld.so.cache",
	"/lib",
	"/lib64",
	"/usr/lib",
	"/usr/lib/x86_64-linux-gnu",
}

// Probe verifies that the confinement works and records how. It runs the
// real Options through bwrap with a trivial command: if the fresh procfs
// mounts, that is the preferred form; if the environment denies that
// mount specifically, the host's /proc is bound read-only instead. Any
// other failure means confining is impossible and is an error — better
// before the script is generated and reviewed than after its approval.
//
// When Options.Bwrap is already set, Probe returns it immediately, so
// probing twice is cheap.
func Probe(opts Options, cmdpath string) (Bwrap, error) {
	if opts.Bwrap.Path != "" {
		return opts.Bwrap, nil
	}

	path, err := exec.LookPath("bwrap")
	if err != nil {
		return Bwrap{}, fmt.Errorf("cannot find bwrap in $PATH")
	}

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
		return Bwrap{Path: path}, nil
	}
	if !bytes.Contains(stderr, []byte("Can't mount proc")) {
		// Only the proc mount denial is fixed by changing the proc
		// form; anything else means confining is impossible.
		return Bwrap{}, fmt.Errorf("cannot confine with bwrap: %w", err)
	}

	if _, err := quiet(true); err != nil {
		return Bwrap{}, fmt.Errorf("cannot confine with bwrap: %w", err)
	}
	return Bwrap{Path: path, HostProc: true}, nil
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

	// Machinery: the command being confined.
	args = append(args, "--ro-bind", cmdpath, cmdpath)
	for _, path := range implicit {
		if _, err := os.Stat(path); err != nil {
			continue // not on this system
		}
		args = append(args, "--ro-bind", path, path)
	}

	for _, path := range opts.Readable {
		args = append(args, "--ro-bind", path, path)
	}
	for _, path := range opts.Writable {
		args = append(args, "--bind", path, path)
	}

	// The spread cannot mix with individual arguments, so -- and the
	// command go on first, then the spread.
	return append(append(args, "--", cmdpath), cmdargs...), nil
}
