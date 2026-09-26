// Package cli implements the command-line argument parsing for now.
package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Options is the parsed form of the command line.
type Options struct {
	// Request is the natural language request that will be sent to the
	// model for creating a script.
	Request string

	// Args holds the arguments provided after the request, in order.
	Args []string

	// With are external command names from $PATH allowed in the script.
	With []string

	// Yes auto-approves the generated script.
	Yes bool

	// Quiet auto-approves like Yes, and also hides the script before
	// running it.
	Quiet bool
}

// ParseError is returned for invalid command lines; its message is meant to
// be shown directly to the user.
type ParseError struct{ msg string }

func (e *ParseError) Error() string { return e.msg }

func parseErrf(format string, args ...any) error {
	return &ParseError{msg: fmt.Sprintf(format, args...)}
}

// usage is shown when no arguments are given or -h is used.
const usage = `
Usage:

  now [-y] [-q] [-w cmd,...] "<request>" [<arg> ...]

Arguments:

  <request>      Natural language request for operation to perform.
  <arg>          Arguments made available to the script, in order.
  -y             Auto-approve the generated script without asking.
  -q             Auto-approve and also hide the script before running it.
  -w cmd,...     Comma-separated external command names allowed to the script.
  -              Reads request or arguments from stdin, in the specified position.
`

// Parse parses the argument list (without the program name), reading stdin
// when a "-" placeholder is given.
func Parse(argv []string, stdin io.Reader) (*Options, error) {
	opts := &Options{}

	rest, err := parseFlags(argv, opts)
	if err != nil {
		return nil, err
	}
	if len(rest) == 0 {
		return nil, parseErrf("missing request\n\n%s", usage)
	}

	// Exactly one "-" placeholder is allowed, either as the request or as
	// a path; stdin is read once and injected in its place.
	stdinUsed := false
	readStdinOnce := func() ([]string, error) {
		if stdinUsed {
			return nil, parseErrf(`only one "-" is allowed`)
		}
		stdinUsed = true
		return readStdin(stdin)
	}

	if rest[0] == "-" {
		lines, err := readStdinOnce()
			if err != nil {
			return nil, err
		}
			if len(lines) == 0 {
			return nil, parseErrf("empty request on stdin")
		}
		rest[0] = strings.Join(lines, "\n")
	}
	opts.Request = rest[0]
	if strings.TrimSpace(opts.Request) == "" {
		return nil, parseErrf("empty request")
	}

	for _, arg := range rest[1:] {
		if arg == "-" {
			lines, err := readStdinOnce()
			if err != nil {
				return nil, err
			}
			opts.Args = append(opts.Args, lines...)
			continue
		}
		opts.Args = append(opts.Args, arg)
	}

	return opts, nil
}

// readStdin returns the stdin lines, dropping empty ones.
func readStdin(r io.Reader) ([]string, error) {
	var lines []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			lines = append(lines, line)
		}
	}
	if err := sc.Err(); err != nil {
			return nil, parseErrf("cannot read stdin: %v", err)
	}
	return lines, nil
}

// parseFlags extracts leading flags; the request and paths follow.
func parseFlags(argv []string, opts *Options) ([]string, error) {
	i := 0
	for i < len(argv) {
		arg := argv[i]
		switch {
		case arg == "-h" || arg == "--help":
			return nil, &HelpRequested{}
		case arg == "-y":
			opts.Yes = true
		case arg == "-q":
			opts.Quiet = true
		case arg == "-w":
			if i+1 >= len(argv) {
				return nil, parseErrf("-w requires a comma-separated list of commands")
			}
			i++
			if err := addWith(opts, argv[i]); err != nil {
				return nil, err
			}
		case strings.HasPrefix(arg, "-w="):
			if err := addWith(opts, strings.TrimPrefix(arg, "-w=")); err != nil {
				return nil, err
			}
		case strings.HasPrefix(arg, "-") && arg != "-":
			return nil, parseErrf("unknown flag %q", arg)
		default:
			return argv[i:], nil
		}
		i++
	}
	return nil, nil
}

func addWith(opts *Options, list string) error {
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			return parseErrf("-w: empty command name")
		}
		opts.With = append(opts.With, name)
	}
	return nil
}

// HelpRequested is returned when -h or --help is given.
type HelpRequested struct{}

func (*HelpRequested) Error() string { return usage }


