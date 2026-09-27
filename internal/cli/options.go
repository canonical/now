// Package cli implements the command-line argument parsing for now.
package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/niemeyer/now/internal/prompt"
)

// Options is the parsed form of the command line.
type Options struct {
	// Request is the natural language request that will be sent to the
	// model for creating a script.
	Request string

	// Args holds the arguments provided after the request, in order.
	Args []string

	// Commands are external commands from $PATH allowed in the
	// script, with their --help output.
	Commands []prompt.Command

	// Yes auto-approves the generated script.
	Yes bool

	// Quiet auto-approves like Yes, and also hides the script before
	// running it.
	Quiet bool

	// Trace prints each script command to stderr as it executes,
	// like the shell's -x.
	Trace bool

	// Sandbox confines the script execution with bwrap even without
	// other confining flags.
	Sandbox bool

	// Readable holds the paths the script may read, when confined.
	Readable []string

	// Writable holds the paths the script may read and write, when
	// confined.
	Writable []string

	// Network keeps the network available when confined; confinement
	// unshares it otherwise.
	Network bool
}

// ParseError is returned for invalid command lines; its message is meant to
// be shown directly to the user.
type ParseError struct{ msg string }

func (e *ParseError) Error() string { return e.msg }

func parseErrf(format string, args ...any) error {
	return &ParseError{msg: fmt.Sprintf(format, args...)}
}

const usage = `Usage:

  now [options] "<request>" [<arg> ...]

The now command generates a shell script to perform the requested
operation, prints it for approval, and then executes it in busybox.
An LLM model is used to genereate the script while only having access
to the information explicitly provided via arguments and stdin. No
tool calls, no multiple turns, no local access, the LLM must one-shot it
with the context provided.

Arguments and standard input lines are flattened into a single ordered
sequence that is made available to the model and the script in "$@".

  $ echo f1 f2 | now "cp [foo] files to [bar] dirs" [foo] - [bar] /d1 /d2
  for f in f1 f2; do
    for d in /d1 /d2; do
      cp "$f" "$d/"
    done
  done
  [ ENTER | CTRL-C ]

The key to the success when using now is realizing that arguments have no
explicit meaning. It's up to the request and the model to define it, and most
often the model can tell what is meant with no further help. 

Options:

  <request>       Natural language request for operation to perform.
  <arg>           Data made available to the model and script, in order.
  -               Read either the request or the arguments from stdin.
  -y              Auto-approve the generated script without asking.
  -q              Auto-approve and also hide the script before running it.
  -t              Trace each script command to stderr as it executes.
  -c cmd,...      External command names from $PATH for the script to use.
  -s              Force sandbox mode even without any -r and -w paths.
  -r path -r ...  Force sandbox mode, allow read-only access to path.
  -w path -w ...  Force sandbox mode, allow read-write access to path.
  -n              Allow network usage when in sandbox mode.
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
		return nil, parseErrf("missing request")
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
		case arg == "-t":
			opts.Trace = true
		case arg == "-s":
			opts.Sandbox = true
		// -r and -w stay plain and separate: -w implies read too, and we
		// do not want to take over -x, as it may come some day with an
		// execution semantics that bwrap alone cannot express.
		case arg == "-r":
			if i+1 >= len(argv) {
				return nil, parseErrf("-r requires a path")
			}
			i++
			if err := addReadable(opts, argv[i]); err != nil {
				return nil, err
			}
		case strings.HasPrefix(arg, "-r="):
			if err := addReadable(opts, strings.TrimPrefix(arg, "-r=")); err != nil {
				return nil, err
			}
		case arg == "-w":
			if i+1 >= len(argv) {
				return nil, parseErrf("-w requires a path")
			}
			i++
			if err := addWritable(opts, argv[i]); err != nil {
				return nil, err
			}
		case strings.HasPrefix(arg, "-w="):
			if err := addWritable(opts, strings.TrimPrefix(arg, "-w=")); err != nil {
				return nil, err
			}
		case arg == "-n":
			opts.Network = true
		case arg == "-c":
			if i+1 >= len(argv) {
				return nil, parseErrf("-c requires a comma-separated list of commands")
			}
			i++
			if err := addCommands(opts, argv[i]); err != nil {
				return nil, err
			}
		case strings.HasPrefix(arg, "-c="):
			if err := addCommands(opts, strings.TrimPrefix(arg, "-c=")); err != nil {
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

// addReadable records path as readable, verifying it exists so a typo
// fails before any API call. The grant is stored as an absolute path:
// bwrap resolves bind destinations against the sandbox root, where a
// relative path breaks the confinement.
func addReadable(opts *Options, path string) error {
	if _, err := os.Stat(path); err != nil {
		return parseErrf("cannot read %s: no such file or directory", path)
	}
	opts.Readable = append(opts.Readable, absPath(path))
	return nil
}

// addWritable records path as writable, verifying it exists so a typo
// fails before any API call. The grant is stored as an absolute path,
// like addReadable.
func addWritable(opts *Options, path string) error {
	if _, err := os.Stat(path); err != nil {
		return parseErrf("cannot write %s: no such file or directory", path)
	}
	opts.Writable = append(opts.Writable, absPath(path))
	return nil
}

// absPath resolves p against the working directory.
func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p // unreachable on POSIX: Abs only fails on Getwd failure
	}
	return abs
}

func addCommands(opts *Options, list string) error {
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			return parseErrf("-w: empty command name")
		}
		cmd, err := findCommand(name)
		if err != nil {
			return err
		}
		opts.Commands = append(opts.Commands, cmd)
	}
	return nil
}

// findCommand resolves name in $PATH and captures its --help output. A
// command that does not support --help is still accepted; it just carries
// no help for the prompt.
func findCommand(name string) (prompt.Command, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return prompt.Command{}, parseErrf("cannot find command %q in $PATH", name)
	}
	out, err := exec.Command(path, "--help").Output()
	if err != nil {
		return prompt.Command{Name: name, Path: path}, nil
	}
	return prompt.Command{Name: name, Path: path, Help: string(out)}, nil
}

// HelpRequested is returned when -h or --help is given.
type HelpRequested struct{}

func (*HelpRequested) Error() string { return usage }


