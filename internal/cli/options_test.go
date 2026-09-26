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

	opts := mustParse(t, []string{"-w", "nohelputil", "q", "a.txt"})
	assertEqual(t, "len(Commands)", len(opts.Commands), 1)
	assertEqual(t, "Name", opts.Commands[0].Name, "nohelputil")
	assertEqual(t, "Help", opts.Commands[0].Help, "")
	if !filepath.IsAbs(opts.Commands[0].Path) {
		t.Errorf("Path = %q, want absolute", opts.Commands[0].Path)
	}
}

func TestParseCommandsFlag(t *testing.T) {
	withFakeCommands(t, "fakeone", "faketwo")

	opts := mustParse(t, []string{"-w", "fakeone,faketwo", "q", "a.txt"})
	dir := filepath.Dir(opts.Commands[0].Path)
	assertEqual(t, "Commands", opts.Commands, []prompt.Command{
		{Name: "fakeone", Path: filepath.Join(dir, "fakeone"), Help: "usage: fakeone [options]\n"},
		{Name: "faketwo", Path: filepath.Join(dir, "faketwo"), Help: "usage: faketwo [options]\n"},
	})

	opts = mustParse(t, []string{"-w=fakeone , faketwo", "q", "a.txt"})
	assertEqual(t, "len(Commands)", len(opts.Commands), 2)
}

func TestParseUnknownCommand(t *testing.T) {
	// A -w command that is not in $PATH is a hard error.
	_, err := cli.Parse([]string{"-w", "nosuchcmd", "q", "a.txt"}, strings.NewReader(""))
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

func TestParseTraceFlag(t *testing.T) {
	opts := mustParse(t, []string{"-x", "q", "a.txt"})
	if !opts.Trace {
		t.Errorf("-x: Trace = false, want true")
	}

	opts = mustParse(t, []string{"q", "a.txt"})
	if opts.Trace {
		t.Errorf("without -x: Trace = true, want false")
	}

	// -x after the request is a path, not a flag.
	opts = mustParse(t, []string{"q", "-x"})
	if opts.Trace {
		t.Errorf("-x after request: Trace = true, want false")
	}
	assertEqual(t, "Args", opts.Args, []string{"-x"})
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
		{"-w missing value", []string{"-w"}},
		{"-w empty name", []string{"-w", "curl,,jq", "q", "a.txt"}},
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
	opts, err := cli.Parse([]string{"q", "-w", "a.txt"}, strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Args", opts.Args, []string{"-w", "a.txt"})
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

