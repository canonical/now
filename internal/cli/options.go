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

// Package cli implements the command-line argument parsing for now.
package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/canonical/now/internal/prompt"
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

	// Buffered captures the script's output and shows it only when the
	// script fails.
	Buffered bool

	// Format selects the output mode. Empty means the default run
	// cycle (generate, approve, execute). "sh" means generate the
	// script and write it out instead of presenting or running it. Any
	// other value means generate formatted content in that format and
	// write it out. Resolved from -f, or inferred from -o's extension
	// when -f is absent.
	Format string

	// Output is the file path to write the generated script or
	// formatted content to. Empty means write to stdout. Set by -o.
	Output string
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

  $ echo f1 f2 | now "cp FOO files to BAR dirs" FOO: - BAR: /d1 /d2
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
  <request>         Natural language request.
  <arg>             Data made available to the model and script, ordered.
  -                 Read either the request or the arguments from stdin.

Running control:
  -y                Auto-approve the generated script without asking.
  -q                Auto-approve and also hide the script before running it.
  -t                Trace each script command to stderr as it executes.
  -b                Buffer script output and only show it on failure.
  -c <cmd>,...      External command names from $PATH for the script to use.

Sandbox mode:
  -s                Enforce sandbox mode even without -r -w -n.
  -r <path> -r ...  Enforce sandbox mode and allow read-only access to path.
  -w <path> -w ...  Enforce sandbox mode and allow read-write access to path.
  -n                Enforce sandbox mode and allow network usage.

Output mode:
  -o <path>         Just write the content. Default -f from file extension.
  -f <format>       Just output content in the given format.

Boolean flags may be bundled together.
`

// Parse parses the argument list (without the program name), reading stdin
// when a "-" placeholder is given.
func Parse(argv []string, stdin io.Reader) (*Options, error) {
	opts := &Options{}

	rest, err := parseFlags(argv, opts)
	if err != nil {
		return nil, err
	}
	if err := resolveFormat(opts); err != nil {
		return nil, err
	}
	if err := checkFormatConflicts(opts); err != nil {
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
// Boolean flags may be bundled, as in -qt; flags taking a value cannot.
func parseFlags(argv []string, opts *Options) ([]string, error) {
	i := 0
	for i < len(argv) {
		arg := argv[i]
		switch {
		case arg == "-h" || arg == "--help":
			// -h and --help are intentionally NOT listed in the usage
			// text above. They are kept as quiet conveniences; do not
			// add them to the documented options.
			return nil, &HelpRequested{}
		// -r, -w, and -c stay plain and separate: -w implies read too,
		// and we do not want to take over -x, as it may come some day
		// with an execution semantics that bwrap alone cannot express.
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
		case arg == "-f":
			if i+1 >= len(argv) {
				return nil, parseErrf("-f requires a format")
			}
			i++
			if err := setFormat(opts, argv[i]); err != nil {
				return nil, err
			}
		case strings.HasPrefix(arg, "-f="):
			if err := setFormat(opts, strings.TrimPrefix(arg, "-f=")); err != nil {
				return nil, err
			}
		case arg == "-o":
			if i+1 >= len(argv) {
				return nil, parseErrf("-o requires a file path")
			}
			i++
			opts.Output = argv[i]
		case strings.HasPrefix(arg, "-o="):
			opts.Output = strings.TrimPrefix(arg, "-o=")
		case len(arg) > 1 && arg[0] == '-' && strings.TrimRight(arg, "abcdefghijklmnopqrstuvwxyz") == "-":
			for _, flag := range arg[1:] {
				switch flag {
				case 'y':
					opts.Yes = true
				case 'q':
					opts.Quiet = true
				case 't':
					opts.Trace = true
				case 'b':
					opts.Buffered = true
				case 's':
					opts.Sandbox = true
				case 'n':
					opts.Network = true
				default:
					return nil, parseErrf("%q is not a boolean flag: %q", string(flag), arg)
				}
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

// formatRe constrains the -f value and the -o extension: lowercase
// alphanumerics, optionally separated by dots or dashes.
var formatRe = regexp.MustCompile(`^[a-z0-9]+([.-][a-z0-9]+)*$`)

// setFormat validates and records the -f value. The value is taken
// verbatim: -f does not lowercase, so an uppercase or otherwise
// invalid value is an error rather than a silent fix. "shell" is a
// quiet convenience alias for "sh" (the only one; "bash" is not, as
// it implies a non-POSIX syntax). That alias is undocumented.
func setFormat(opts *Options, val string) error {
	if val == "" {
		return parseErrf("-f requires a format")
	}
	if val == "shell" {
		val = "sh"
	}
	if !formatRe.MatchString(val) {
		return parseErrf("-f: invalid format %q", val)
	}
	opts.Format = val
	return nil
}

// resolveFormat infers the format from -o's extension when -f was not
// given. The extension is lowercased before matching the format
// constraint; an empty extension means "sh" (write the script). An
// extension that does not match the constraint is an error, since the
// user asked for a file we cannot classify.
func resolveFormat(opts *Options) error {
	if opts.Format != "" || opts.Output == "" {
		return nil
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(opts.Output), "."))
	if ext == "" {
		opts.Format = "sh"
		return nil
	}
	if !formatRe.MatchString(ext) {
		return parseErrf("cannot infer output format from -o, use -f")
	}
	opts.Format = ext
	return nil
}

// checkFormatConflicts rejects flags that make no sense once a format
// is selected: the run-control and confinement flags all assume the
// script will execute, which write mode never does. -c is allowed only
// for "sh", where it feeds the script prompt as in run mode.
func checkFormatConflicts(opts *Options) error {
	if opts.Format == "" {
		return nil
	}
	for _, bad := range []struct {
		flag string
		set  bool
	}{
		{"-y", opts.Yes},
		{"-q", opts.Quiet},
		{"-t", opts.Trace},
		{"-b", opts.Buffered},
		{"-s", opts.Sandbox},
		{"-n", opts.Network},
		{"-r", len(opts.Readable) > 0},
		{"-w", len(opts.Writable) > 0},
	} {
		if bad.set {
			return parseErrf("cannot use %s with -f or -o", bad.flag)
		}
	}
	if opts.Format != "sh" && len(opts.Commands) > 0 {
		return parseErrf("cannot use -c with -f or -o")
	}
	return nil
}

// findCommand resolves name in $PATH and captures its --help output. A
// command that does not support --help is still accepted; it just carries
// no help for the prompt. HELP_FOR_AGENT=1 is set in the child environment
// so a command that wants to tailor its --help output for automated use
// (e.g. a terser format, or a machine-readable summary) can detect it.
func findCommand(name string) (prompt.Command, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return prompt.Command{}, parseErrf("cannot find command %q in $PATH", name)
	}
	cmd := exec.Command(path, "--help")
	cmd.Env = append(os.Environ(), "HELP_FOR_AGENT=1")
	out, err := cmd.Output()
	if err != nil {
		return prompt.Command{Name: name, Path: path}, nil
	}
	return prompt.Command{Name: name, Path: path, Help: string(out)}, nil
}

// HelpRequested is returned when -h or --help is given. Both forms are
// intentionally undocumented in the usage text; do not add them to the
// documented options.
type HelpRequested struct{}

func (*HelpRequested) Error() string { return usage }


