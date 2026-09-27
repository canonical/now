// Copyright (c) 2026 Canonical Inc.
// Originally by Gustavo Niemeyer.
// Use of source code is governed by the MIT-style license in the LICENSE file.

package cli_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/niemeyer/now/internal/cli"
	"github.com/niemeyer/now/internal/prompt"
)

// assertEqual fails the test when got and want are not deeply equal.
func assertEqual[T any](t *testing.T, label string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", label, got, want)
	}
}

func mustParse(t *testing.T, argv []string) *cli.Options {
	t.Helper()
	return mustParseStdin(t, argv, "")
}

func mustParseStdin(t *testing.T, argv []string, stdin string) *cli.Options {
	t.Helper()
	opts, err := cli.Parse(argv, strings.NewReader(stdin))
	if err != nil {
		t.Fatalf("cli.Parse(%q) unexpected error: %v", argv, err)
	}
	return opts
}

func TestParseBasic(t *testing.T) {
	opts := mustParse(t, []string{"change the suffix", "hello-world.txt", "other.txt"})
	assertEqual(t, "Request", opts.Request, "change the suffix")
	assertEqual(t, "Args", opts.Args, []string{"hello-world.txt", "other.txt"})
	assertEqual(t, "Commands", opts.Commands, []prompt.Command(nil))
}

func TestParseStdin(t *testing.T) {
	opts := mustParseStdin(t, []string{"q", "-", "a.txt"}, "s1.txt\n\ns2.txt\n")
	assertEqual(t, "Args", opts.Args, []string{"s1.txt", "s2.txt", "a.txt"})
}

func TestParseStdinSinglePlaceholderOnly(t *testing.T) {
	_, err := cli.Parse([]string{"q", "a", "-", "b", "-", "c"}, strings.NewReader("x.txt\n"))
	if err == nil || !strings.Contains(err.Error(), `only one "-"`) {
		t.Fatalf("expected single-placeholder error, got %v", err)
	}
}

func TestParseStdinRequest(t *testing.T) {
	opts := mustParseStdin(t, []string{"-", "a.txt"}, "the request\n")
	assertEqual(t, "Request", opts.Request, "the request")
	assertEqual(t, "Args", opts.Args, []string{"a.txt"})

	// Multi-line requests are joined with newlines.
	opts = mustParseStdin(t, []string{"-", "a.txt"}, "rename these files\nlike set 2\n")
	assertEqual(t, "Request", opts.Request, "rename these files\nlike set 2")

	_, err := cli.Parse([]string{"-", "a.txt"}, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "empty request on stdin") {
		t.Fatalf("expected empty stdin error, got %v", err)
	}
}

// withFakeCommands puts a temp dir with fake commands on $PATH for the
// test. Each fake prints a fixed --help line.
func withFakeCommands(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		script := "#!/bin/sh\necho \"usage: " + name + " [options]\"\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatalf("cannot write command: %v", err)
		}
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

// withNoHelpCommands is like withFakeCommands, but the commands reject
// --help with a non-zero exit and no output.
func withNoHelpCommands(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		script := "#!/bin/sh\nexit 1\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatalf("cannot write command: %v", err)
		}
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

func TestParseCommandWithoutHelp(t *testing.T) {
	// A command that does not support --help is accepted, with empty
	// help but a resolved path.
	withNoHelpCommands(t, "nohelputil")

	opts := mustParse(t, []string{"-c", "nohelputil", "q", "a.txt"})
	assertEqual(t, "len(Commands)", len(opts.Commands), 1)
	assertEqual(t, "Name", opts.Commands[0].Name, "nohelputil")
	assertEqual(t, "Help", opts.Commands[0].Help, "")
	if !filepath.IsAbs(opts.Commands[0].Path) {
		t.Errorf("Path = %q, want absolute", opts.Commands[0].Path)
	}
}

func TestParseCommandsFlag(t *testing.T) {
	withFakeCommands(t, "fakeone", "faketwo")

	opts := mustParse(t, []string{"-c", "fakeone,faketwo", "q", "a.txt"})
	dir := filepath.Dir(opts.Commands[0].Path)
	assertEqual(t, "Commands", opts.Commands, []prompt.Command{
		{Name: "fakeone", Path: filepath.Join(dir, "fakeone"), Help: "usage: fakeone [options]\n"},
		{Name: "faketwo", Path: filepath.Join(dir, "faketwo"), Help: "usage: faketwo [options]\n"},
	})

	opts = mustParse(t, []string{"-c=fakeone , faketwo", "q", "a.txt"})
	assertEqual(t, "len(Commands)", len(opts.Commands), 2)
}

func TestParseUnknownCommand(t *testing.T) {
	// A -w command that is not in $PATH is a hard error.
	_, err := cli.Parse([]string{"-c", "nosuchcmd", "q", "a.txt"}, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), `cannot find command "nosuchcmd"`) {
		t.Fatalf("expected missing command error, got %v", err)
	}
}

func TestParseRequestOnly(t *testing.T) {
	// Paths are optional; a request alone is valid.
	opts := mustParse(t, []string{"just generate something"})
	assertEqual(t, "Request", opts.Request, "just generate something")
	assertEqual(t, "Args", opts.Args, []string(nil))
}

func TestParseSandboxFlag(t *testing.T) {
	opts := mustParse(t, []string{"-s", "q"})
	if !opts.Sandbox {
		t.Errorf("-s: Sandbox = false, want true")
	}
	assertEqual(t, "Readable", opts.Readable, []string(nil))

	opts = mustParse(t, []string{"q"})
	if opts.Sandbox {
		t.Errorf("without -s: Sandbox = true, want false")
	}
}

func TestParseReadableFlag(t *testing.T) {
	dir := t.TempDir()

	opts := mustParse(t, []string{"-r", dir, "q"})
	assertEqual(t, "Readable", opts.Readable, []string{dir})

	// Repeated flags accumulate.
	opts = mustParse(t, []string{"-r", dir, "-r=" + dir, "q"})
	assertEqual(t, "Readable", opts.Readable, []string{dir, dir})

	// -r after the request is a plain argument.
	opts = mustParse(t, []string{"q", "-r", dir})
	assertEqual(t, "Readable", opts.Readable, []string(nil))
	assertEqual(t, "Args", opts.Args, []string{"-r", dir})
}

func TestParseGrantsAreAbsolute(t *testing.T) {
	// Grants are stored as absolute paths: bwrap resolves bind
	// destinations against the sandbox root, where a relative path
	// breaks the confinement.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("cannot create sub: %v", err)
	}
	t.Chdir(dir)

	opts := mustParse(t, []string{"-r", ".", "q"})
	assertEqual(t, "Readable", opts.Readable, []string{dir})

	opts = mustParse(t, []string{"-w", "sub", "q"})
	assertEqual(t, "Writable", opts.Writable, []string{filepath.Join(dir, "sub")})
}

func TestParseReadableMissingPath(t *testing.T) {
	// A grant on a missing path fails before any API call.
	_, err := cli.Parse([]string{"-r", "/nosuch/path", "q"}, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "cannot read /nosuch/path") {
		t.Fatalf("expected missing path error, got %v", err)
	}
}

func TestParseWritableFlag(t *testing.T) {
	dir := t.TempDir()

	opts := mustParse(t, []string{"-w", dir, "q"})
	assertEqual(t, "Writable", opts.Writable, []string{dir})
	assertEqual(t, "Readable", opts.Readable, []string(nil))

	// Repeated flags accumulate, = form works.
	opts = mustParse(t, []string{"-w", dir, "-w=" + dir, "q"})
	assertEqual(t, "Writable", opts.Writable, []string{dir, dir})

	// -w after the request is a plain argument.
	opts = mustParse(t, []string{"q", "-w", dir})
	assertEqual(t, "Writable", opts.Writable, []string(nil))
	assertEqual(t, "Args", opts.Args, []string{"-w", dir})
}

func TestParseWritableMissingPath(t *testing.T) {
	_, err := cli.Parse([]string{"-w", "/nosuch/path", "q"}, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "cannot write /nosuch/path") {
		t.Fatalf("expected missing path error, got %v", err)
	}
}

func TestParseNetworkFlag(t *testing.T) {
	opts := mustParse(t, []string{"-n", "q"})
	if !opts.Network {
		t.Errorf("-n: Network = false, want true")
	}

	opts = mustParse(t, []string{"q"})
	if opts.Network {
		t.Errorf("without -n: Network = true, want false")
	}
}

func TestParseTraceFlag(t *testing.T) {
	opts := mustParse(t, []string{"-t", "q", "a.txt"})
	if !opts.Trace {
		t.Errorf("-x: Trace = false, want true")
	}

	opts = mustParse(t, []string{"q", "a.txt"})
	if opts.Trace {
		t.Errorf("without -x: Trace = true, want false")
	}

	// -x after the request is a path, not a flag.
	opts = mustParse(t, []string{"q", "-t"})
	if opts.Trace {
		t.Errorf("-x after request: Trace = true, want false")
	}
	assertEqual(t, "Args", opts.Args, []string{"-t"})
}

func TestParseYesFlag(t *testing.T) {
	opts := mustParse(t, []string{"-y", "q", "a.txt"})
	if !opts.Yes {
		t.Errorf("-y: Yes = false, want true")
	}

	opts = mustParse(t, []string{"q", "a.txt"})
	if opts.Yes {
		t.Errorf("without -y: Yes = true, want false")
	}

	// -y after the request is a path, not a flag.
	opts = mustParse(t, []string{"q", "-y"})
	if opts.Yes {
		t.Errorf("-y after request: Yes = true, want false")
	}
	assertEqual(t, "Args", opts.Args, []string{"-y"})
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		argv []string
	}{
		{"no args", nil},
		{"empty request", []string{"  ", "a.txt"}},
		{"unknown flag", []string{"-z", "q", "a.txt"}},
		{"-c missing value", []string{"-c"}},
		{"-c empty name", []string{"-c", "curl,,jq", "q", "a.txt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := cli.Parse(tt.argv, strings.NewReader(""))
			var pe *cli.ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("expected cli.ParseError, got %v", err)
			}
		})
	}
}

func TestParseFlagAfterRequestIsPath(t *testing.T) {
	// -w after the request is just a path, not a flag.
	opts, err := cli.Parse([]string{"q", "-c", "a.txt"}, strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Args", opts.Args, []string{"-c", "a.txt"})
}

func TestParseHelp(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		_, err := cli.Parse([]string{flag}, strings.NewReader(""))
		var hr *cli.HelpRequested
		if !errors.As(err, &hr) {
			t.Errorf("cli.Parse(%q): expected cli.HelpRequested, got %v", flag, err)
		}
	}
}

func TestColonArgumentsArePaths(t *testing.T) {
	// With no set syntax, colon-containing arguments are ordinary paths.
	opts := mustParse(t, []string{"q", "9:", "b.txt", "1: foo/a.txt", "1::", "0:", "10:"})
	assertEqual(t, "Args", opts.Args, []string{"9:", "b.txt", "1: foo/a.txt", "1::", "0:", "10:"})
}

