package cli_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/niemeyer/now/internal/cli"
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
	assertEqual(t, "Sets", opts.Sets, map[int][]string{
		0: {"hello-world.txt", "other.txt"},
	})
	assertEqual(t, "With", opts.With, []string(nil))
}

func TestParseSets(t *testing.T) {
	opts := mustParse(t, []string{"rename 1 like 2", "1:", "foo/a.txt", "2:", "bar/b.txt", "bar/c.txt"})
	assertEqual(t, "Sets[1]", opts.Sets[1], []string{"foo/a.txt"})
	assertEqual(t, "Sets[2]", opts.Sets[2], []string{"bar/b.txt", "bar/c.txt"})
}

func TestParseSetNameInlineIsPath(t *testing.T) {
	// Only the exact "N:" form is a set name; "1: foo/a.txt" is a path.
	opts := mustParse(t, []string{"q", "1: foo/a.txt", "b.txt"})
	assertEqual(t, "Sets[0]", opts.Sets[0], []string{"1: foo/a.txt", "b.txt"})
	assertEqual(t, "Sets[1]", opts.Sets[1], []string(nil))
}

func TestParseMixedSetsAndPlain(t *testing.T) {
	opts := mustParse(t, []string{"q", "plain.txt", "1:", "a.txt", "b.txt"})
	assertEqual(t, "Sets[0]", opts.Sets[0], []string{"plain.txt"})
	assertEqual(t, "Sets[1]", opts.Sets[1], []string{"a.txt", "b.txt"})
}

func TestParseStdin(t *testing.T) {
	opts := mustParseStdin(t, []string{"q", "-", "a.txt"}, "s1.txt\n\ns2.txt\n")
	assertEqual(t, "Sets[0]", opts.Sets[0], []string{"s1.txt", "s2.txt", "a.txt"})

	opts = mustParseStdin(t, []string{"q", "1:", "-", "a.txt"}, "s1.txt\n")
	assertEqual(t, "Sets[1]", opts.Sets[1], []string{"s1.txt", "a.txt"})
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
	assertEqual(t, "Sets[0]", opts.Sets[0], []string{"a.txt"})

	// Multi-line requests are joined with newlines.
	opts = mustParseStdin(t, []string{"-", "a.txt"}, "rename these files\nlike set 2\n")
	assertEqual(t, "Request", opts.Request, "rename these files\nlike set 2")

	_, err := cli.Parse([]string{"-", "a.txt"}, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "empty request on stdin") {
		t.Fatalf("expected empty stdin error, got %v", err)
	}
}

func TestParseWithFlag(t *testing.T) {
	opts := mustParse(t, []string{"-w", "curl,jq", "q", "a.txt"})
	assertEqual(t, "With", opts.With, []string{"curl", "jq"})

	opts = mustParse(t, []string{"-w=curl , jq", "q", "a.txt"})
	assertEqual(t, "With", opts.With, []string{"curl", "jq"})
}

func TestParseRequestOnly(t *testing.T) {
	// Paths are optional; a request alone is valid.
	opts := mustParse(t, []string{"just generate something"})
	assertEqual(t, "Request", opts.Request, "just generate something")
	assertEqual(t, "Sets", opts.Sets, map[int][]string{})
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
	assertEqual(t, "Sets[0]", opts.Sets[0], []string{"-y"})
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		argv []string
	}{
		{"no args", nil},
		{"empty request", []string{"  ", "a.txt"}},
		{"unknown flag", []string{"-x", "q", "a.txt"}},
		{"-w missing value", []string{"-w"}},
		{"-w empty name", []string{"-w", "curl,,jq", "q", "a.txt"}},
		{"duplicate set", []string{"q", "1:", "a", "1:", "b"}},
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
	assertEqual(t, "Sets[0]", opts.Sets[0], []string{"-w", "a.txt"})
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

func TestSetsRestrictedToDigits(t *testing.T) {
	// Only the exact "N:" form introduces a set.
	opts := mustParse(t, []string{"q", "9:", "b.txt", "1:", "foo:", "x/y:"})
	assertEqual(t, "Sets[9]", opts.Sets[9], []string{"b.txt"})
	assertEqual(t, "Sets[1]", opts.Sets[1], []string{"foo:", "x/y:"})
}

func TestAmbiguousSetNamesRejected(t *testing.T) {
	// Any "N:" form is a set name; N < 1 or N > 9 is rejected rather than
	// silently treated as a path.
	for _, arg := range []string{"0:", "10:", "42:"} {
		_, err := cli.Parse([]string{"q", "a.txt", arg}, strings.NewReader(""))
		if err == nil || !strings.Contains(err.Error(), "invalid set name") {
			t.Errorf("Parse with %q: expected invalid set name error, got %v", arg, err)
		}
	}
}

func TestSetNameLikePathsAccepted(t *testing.T) {
	// Arguments with content after the colon are ordinary paths.
	opts := mustParse(t, []string{"q", "9: b.txt", "1: foo/a.txt", "1::"})
	assertEqual(t, "Sets[0]", opts.Sets[0], []string{"9: b.txt", "1: foo/a.txt", "1::"})
}

