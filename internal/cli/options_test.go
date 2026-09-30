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

package cli_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/canonical/now/internal/cli"
	"github.com/canonical/now/internal/prompt"
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

func TestParseCommandHelpForAgent(t *testing.T) {
	// findCommand sets HELP_FOR_AGENT=1 when calling --help, so a
	// command can tailor its output for automated consumption.
	dir := t.TempDir()
	script := "#!/bin/sh\n[ \"$HELP_FOR_AGENT\" = 1 ] && echo \"AGENT\" || echo \"HUMAN\"\n"
	if err := os.WriteFile(filepath.Join(dir, "agentutil"), []byte(script), 0o755); err != nil {
		t.Fatalf("cannot write command: %v", err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	opts := mustParse(t, []string{"-c", "agentutil", "q", "a.txt"})
	assertEqual(t, "Help", opts.Commands[0].Help, "AGENT\n")
}

func TestParseRequestOnly(t *testing.T) {
	// Paths are optional; a request alone is valid.
	opts := mustParse(t, []string{"just generate something"})
	assertEqual(t, "Request", opts.Request, "just generate something")
	assertEqual(t, "Args", opts.Args, []string(nil))
}

func TestParseBundledFlags(t *testing.T) {
	// Boolean flags bundle: -qt sets both, in any order and mix.
	opts := mustParse(t, []string{"-qt", "q"})
	if !opts.Quiet || !opts.Trace {
		t.Errorf("-qt: Quiet=%v Trace=%v, want both true", opts.Quiet, opts.Trace)
	}

	opts = mustParse(t, []string{"-tq", "q"})
	if !opts.Quiet || !opts.Trace {
		t.Errorf("-tq: Quiet=%v Trace=%v, want both true", opts.Quiet, opts.Trace)
	}

	opts = mustParse(t, []string{"-ytsn", "q"})
	if !opts.Yes || !opts.Trace || !opts.Sandbox || !opts.Network {
		t.Errorf("-ytsn: not all flags set")
	}

	// A bundle with a non-boolean letter names it, so -r and friends
	// make sense in the message.
	_, err := cli.Parse([]string{"-qx", "q"}, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), `"x" is not a boolean flag: "-qx"`) {
		t.Fatalf("expected unknown flag error, got %v", err)
	}
	_, err = cli.Parse([]string{"-qr", "q"}, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), `"r" is not a boolean flag: "-qr"`) {
		t.Fatalf("expected non-boolean flag error, got %v", err)
	}

	// Bundles are still only recognized before the request.
	opts = mustParse(t, []string{"q", "-qt"})
	if opts.Quiet || opts.Trace {
		t.Errorf("bundle after request parsed as flags")
	}
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
	_, err := cli.Parse([]string{"--help"}, strings.NewReader(""))
	var hr *cli.HelpRequested
	if !errors.As(err, &hr) {
		t.Errorf("cli.Parse(--help): expected cli.HelpRequested, got %v", err)
	}
}

func TestParseHelpShort(t *testing.T) {
	// -h is an undocumented alias for --help (see options.go).
	_, err := cli.Parse([]string{"-h"}, strings.NewReader(""))
	var hr *cli.HelpRequested
	if !errors.As(err, &hr) {
		t.Errorf("cli.Parse(-h): expected cli.HelpRequested, got %v", err)
	}
}

func TestColonArgumentsArePaths(t *testing.T) {
	// With no set syntax, colon-containing arguments are ordinary paths.
	opts := mustParse(t, []string{"q", "9:", "b.txt", "1: foo/a.txt", "1::", "0:", "10:"})
	assertEqual(t, "Args", opts.Args, []string{"9:", "b.txt", "1: foo/a.txt", "1::", "0:", "10:"})
}

func TestParseFormatFlag(t *testing.T) {
	opts := mustParse(t, []string{"-f", "json", "q"})
	assertEqual(t, "Format", opts.Format, "json")
	assertEqual(t, "Output", opts.Output, "")

	// = form.
	opts = mustParse(t, []string{"-f=md", "q"})
	assertEqual(t, "Format", opts.Format, "md")

	// "sh" is a regular format value here.
	opts = mustParse(t, []string{"-f", "sh", "q"})
	assertEqual(t, "Format", opts.Format, "sh")

	// "shell" is a quiet alias for "sh" (the only one; "bash" is not,
	// as it implies a non-POSIX syntax).
	opts = mustParse(t, []string{"-f", "shell", "q"})
	assertEqual(t, "Format", opts.Format, "sh")

	// Dotted and dashed formats are allowed.
	opts = mustParse(t, []string{"-f", "a.b", "q"})
	assertEqual(t, "Format", opts.Format, "a.b")
	opts = mustParse(t, []string{"-f", "a-b", "q"})
	assertEqual(t, "Format", opts.Format, "a-b")
	opts = mustParse(t, []string{"-f", "a.b-c", "q"})
	assertEqual(t, "Format", opts.Format, "a.b-c")

	// Single char and 8 chars are valid boundaries.
	opts = mustParse(t, []string{"-f", "a", "q"})
	assertEqual(t, "Format", opts.Format, "a")
	opts = mustParse(t, []string{"-f", "12345678", "q"})
	assertEqual(t, "Format", opts.Format, "12345678")
}

func TestParseFormatFlagValidation(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want string
	}{
		{"missing value", []string{"-f"}, "-f requires a format"},
		{"empty value", []string{"-f=", "q"}, "-f requires a format"},
		{"uppercase rejected", []string{"-f", "JSON", "q"}, "invalid format"},
		{"dot only rejected", []string{"-f", ".", "q"}, "invalid format"},
		{"dash only rejected", []string{"-f", "-", "q"}, "invalid format"},
		{"leading dot rejected", []string{"-f", ".json", "q"}, "invalid format"},
		{"trailing dot rejected", []string{"-f", "json.", "q"}, "invalid format"},
		{"underscore rejected", []string{"-f", "a_b", "q"}, "invalid format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := cli.Parse(tt.argv, strings.NewReader(""))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
		})
	}
}

func TestParseOutputFlagInfersFormat(t *testing.T) {
	// -o with an extension infers the format (lowercased).
	opts := mustParse(t, []string{"-o", "notes.md", "q"})
	assertEqual(t, "Format", opts.Format, "md")
	assertEqual(t, "Output", opts.Output, "notes.md")

	// Uppercase extension is lowercased before matching.
	opts = mustParse(t, []string{"-o", "notes.JSON", "q"})
	assertEqual(t, "Format", opts.Format, "json")

	// filepath.Ext returns only the last extension.
	opts = mustParse(t, []string{"-o", "x.tar.gz", "q"})
	assertEqual(t, "Format", opts.Format, "gz")
}

func TestParseOutputNoExtensionDefaultsSh(t *testing.T) {
	// -o with no extension defaults to "sh".
	opts := mustParse(t, []string{"-o", "notes", "q"})
	assertEqual(t, "Format", opts.Format, "sh")
	assertEqual(t, "Output", opts.Output, "notes")

	// A dotfile (extension only, no stem) has an extension, not "sh".
	opts = mustParse(t, []string{"-o", ".sh", "q"})
	assertEqual(t, "Format", opts.Format, "sh")
}

func TestParseOutputInvalidExtensionErrors(t *testing.T) {
	// An extension that does not match the format constraint is an
	// error, since the user asked for a file we cannot classify.
	_, err := cli.Parse([]string{"-o", "notes.a_b", "q"}, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "cannot infer output format from -o") {
		t.Fatalf("got %v, want infer error", err)
	}
}

func TestParseFormatOverridesOutputExtension(t *testing.T) {
	// -f always wins over -o's extension; no cross-check.
	opts := mustParse(t, []string{"-f", "json", "-o", "x.md", "q"})
	assertEqual(t, "Format", opts.Format, "json")
	assertEqual(t, "Output", opts.Output, "x.md")
}

func TestParseFormatConflicts(t *testing.T) {
	// Run-control and confinement flags conflict with -f/-o.
	tests := []struct {
		name string
		argv []string
		want string
	}{
		{"-f -y", []string{"-f", "json", "-y", "q"}, "cannot use -y with -f or -o"},
		{"-f -q", []string{"-f", "json", "-q", "q"}, "cannot use -q with -f or -o"},
		{"-f -t", []string{"-f", "json", "-t", "q"}, "cannot use -t with -f or -o"},
		{"-f -b", []string{"-f", "json", "-b", "q"}, "cannot use -b with -f or -o"},
		{"-f -s", []string{"-f", "json", "-s", "q"}, "cannot use -s with -f or -o"},
		{"-f -n", []string{"-f", "json", "-n", "q"}, "cannot use -n with -f or -o"},
		{"-o -y", []string{"-o", "x.md", "-y", "q"}, "cannot use -y with -f or -o"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := cli.Parse(tt.argv, strings.NewReader(""))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
		})
	}
}

func TestParseFormatConflictsGrants(t *testing.T) {
	// -r/-w conflict with -f/-o. The path is validated first, so a
	// missing path reports the path error; an existing one reports
	// the conflict.
	dir := t.TempDir()
	_, err := cli.Parse([]string{"-f", "json", "-r", dir, "q"}, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "cannot use -r with -f or -o") {
		t.Fatalf("got %v, want -r conflict", err)
	}
	_, err = cli.Parse([]string{"-f", "json", "-w", dir, "q"}, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "cannot use -w with -f or -o") {
		t.Fatalf("got %v, want -w conflict", err)
	}
}

func TestParseFormatShAllowsCommands(t *testing.T) {
	// -c is allowed with -f sh: it feeds the script prompt as usual.
	withFakeCommands(t, "fakeone")
	opts := mustParse(t, []string{"-f", "sh", "-c", "fakeone", "q"})
	assertEqual(t, "Format", opts.Format, "sh")
	assertEqual(t, "len(Commands)", len(opts.Commands), 1)

	// "shell" aliases "sh", so it takes the same script path and
	// likewise allows -c.
	opts = mustParse(t, []string{"-f", "shell", "-c", "fakeone", "q"})
	assertEqual(t, "Format", opts.Format, "sh")
	assertEqual(t, "len(Commands)", len(opts.Commands), 1)
}

func TestParseFormatNonShRejectsCommands(t *testing.T) {
	// -c conflicts with a non-sh format: the format prompt has no
	// command surface.
	withFakeCommands(t, "fakeone")
	_, err := cli.Parse([]string{"-f", "json", "-c", "fakeone", "q"}, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "cannot use -c with -f or -o") {
		t.Fatalf("got %v, want -c conflict", err)
	}
}

func TestParseOutputMissingValue(t *testing.T) {
	_, err := cli.Parse([]string{"-o"}, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "-o requires a file path") {
		t.Fatalf("got %v, want -o requires", err)
	}
}

