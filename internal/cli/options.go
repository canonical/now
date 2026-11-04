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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/canonical/now/internal/prompt"
)

// Options is the parsed form of the command line.
type Options struct {
	// Request is the natural language request that will be sent to the
	// model for creating a script.
	Request string

	// PromptFile is the path of a file the request is read from, as
	// an alternative to giving the request on the command line or on
	// stdin. Set by -p; see PromptFile in the usage text.
	PromptFile string

	// Args holds the arguments provided after the request, in order.
	Args []string

	// Commands are external commands from $PATH allowed in the
	// script, with their --help output.
	Commands []prompt.Command

	// Inputs are reference files read with -i, in the order given,
	// with their full content. They are made available to the model
	// under their name, but not to the script via "$@".
	Inputs []prompt.Input

	// Model is the name of the configuration's models entry the run
	// resolves through its provider. Set by -m, empty means "default".
	Model string

	// Yes auto-approves the generated script.
	Yes bool

	// Quiet auto-approves like Yes, and also hides the script before
	// running it.
	Quiet bool

	// Trace prints each script command to stderr as it executes,
	// like the shell's -x; the flag matches.
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

	// FormatFlag is the flag that selected Format: "-f", "-t", or
	// "-o" via extension inference. Empty in run mode. Conflict
	// errors name it, so they reference a flag the user typed.
	FormatFlag string

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
  -                 Read request or args (position defines) from stdin.
  -p <path>         Read the request from the prompt text file.
  -i <path> -i ...  Read the text file as reference data.
  -m <model>        Use named model from configuration instead of default.

Running control:
  -y                Auto-approve the generated script without asking.
  -q                Auto-approve and also hide the script before running it.
  -x                Trace each script command to stderr as it executes.
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
  -t                Same as ` + "`-f plaintext`" + `.

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

	// The request comes either from the -p file or from the first
	// positional; with -p there is no request positional, so every
	// positional below is data. The file is read as a single blob,
	// so its formatting is preserved as given; stdin is read
	// line-by-line with empty lines dropped.
	if opts.PromptFile != "" {
		data, err := os.ReadFile(opts.PromptFile)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, parseErrf("cannot read %s: no such file or directory", opts.PromptFile)
			}
			return nil, parseErrf("cannot read %s: %v", opts.PromptFile, err)
		}
		if strings.TrimSpace(string(data)) == "" {
			return nil, parseErrf("empty request from file: %s", opts.PromptFile)
		}
		opts.Request = string(data)
	} else {
		if len(rest) == 0 {
			return nil, parseErrf("missing request")
		}
		if rest[0] == "-" {
			lines, err := readStdinOnce()
			if err != nil {
				return nil, err
			}
			if len(lines) == 0 {
				return nil, parseErrf("empty request from stdin")
			}
			rest[0] = strings.Join(lines, "\n")
		}
		opts.Request = rest[0]
		if strings.TrimSpace(opts.Request) == "" {
			return nil, parseErrf("empty request")
		}
		rest = rest[1:]
	}

	// The remaining positionals are data; a "-" placeholder injects
	// the stdin lines in its place.
	for _, arg := range rest {
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
// Boolean flags may be bundled, as in -qx; flags taking a value cannot.
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
		// -r, -w, -c, and -i stay plain and separate: -w implies read too.
		case arg == "-i":
			if i+1 >= len(argv) {
				return nil, parseErrf("-i requires a file path")
			}
			i++
			if err := addInput(opts, argv[i]); err != nil {
				return nil, err
			}
		case strings.HasPrefix(arg, "-i="):
			if err := addInput(opts, strings.TrimPrefix(arg, "-i=")); err != nil {
				return nil, err
			}
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
			if err := setFormat(opts, "-f", argv[i]); err != nil {
				return nil, err
			}
		case strings.HasPrefix(arg, "-f="):
			if err := setFormat(opts, "-f", strings.TrimPrefix(arg, "-f=")); err != nil {
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
		case arg == "-p":
			if i+1 >= len(argv) {
				return nil, parseErrf("-p requires a file path")
			}
			i++
			opts.PromptFile = argv[i]
		case strings.HasPrefix(arg, "-p="):
			opts.PromptFile = strings.TrimPrefix(arg, "-p=")
		case arg == "-m":
			if i+1 >= len(argv) {
				return nil, parseErrf("-m requires a model name")
			}
			i++
			if err := setModel(opts, argv[i]); err != nil {
				return nil, err
			}
		case strings.HasPrefix(arg, "-m="):
			if err := setModel(opts, strings.TrimPrefix(arg, "-m=")); err != nil {
				return nil, err
			}
		case len(arg) > 1 && arg[0] == '-' && strings.TrimRight(arg, "abcdefghijklmnopqrstuvwxyz") == "-":
			for _, flag := range arg[1:] {
				switch flag {
				case 'y':
					opts.Yes = true
				case 'q':
					opts.Quiet = true
				case 'x':
					opts.Trace = true
				case 't':
					if err := setFormat(opts, "-t", "plaintext"); err != nil {
						return nil, err
					}
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

// maxInputSize bounds the content of one -i reference file. A
// larger file would dominate the prompt.
const maxInputSize = 16 << 10

// addInput reads the reference file and records it under the path as
// given, so the model refers to the file by that name. The content
// must be valid UTF-8 within 16k.
func addInput(opts *Options, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return parseErrf("cannot read %q: file not found", path)
		}
		return parseErrf("cannot read %q: %v", path, err)
	}
	if info.IsDir() {
		return parseErrf("cannot read %q: it is a directory", path)
	}
	if info.Size() > maxInputSize {
		return parseErrf("cannot read %q: file is larger than 16k", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return parseErrf("cannot read %q: file not found", path)
		}
		return parseErrf("cannot read %q: %v", path, err)
	}
	if !utf8.Valid(data) {
		return parseErrf("cannot read %q: file is not valid UTF-8", path)
	}
	opts.Inputs = append(opts.Inputs, prompt.Input{Name: path, Data: string(data)})
	return nil
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

// setModel records the model name selected by -m. The value must
// be a non-empty bare word; whether it exists is checked when the
// configuration loads, since only it knows the models map. Using -m
// more than once is ambiguous and an error.
func setModel(opts *Options, val string) error {
	if val == "" {
		return parseErrf("-m requires a model name")
	}
	if opts.Model != "" {
		return parseErrf("cannot use -m more than once")
	}
	opts.Model = val
	return nil
}

// formatRe constrains the -f value and the -o extension: lowercase
// alphanumerics, optionally separated by dots or dashes.
var formatRe = regexp.MustCompile(`^[a-z0-9]+([.-][a-z0-9]+)*$`)

// setFormat validates and records the format selected by the given
// flag (-f or -t). The value is verbatim, never lowercased: invalid
// input is an error, not a silent fix. "shell" is a quiet alias for
// "sh" (the only one; "bash" is not, as it implies non-POSIX
// syntax). Setting a format twice is a conflict, whatever the flags.
func setFormat(opts *Options, flag string, val string) error {
	if val == "" {
		return parseErrf("-f requires a format")
	}
	if opts.FormatFlag != "" {
		if flag == opts.FormatFlag {
			return parseErrf("cannot use %s more than once", flag)
		}
		return parseErrf("cannot use %s with %s", flag, opts.FormatFlag)
	}
	if val == "shell" {
		val = "sh"
	}
	if !formatRe.MatchString(val) {
		return parseErrf("-f: invalid format %q", val)
	}
	opts.FormatFlag = flag
	opts.Format = val
	return nil
}

// resolveFormat infers the format from -o's extension when -f was
// not given, and records -o as the selecting flag so conflict
// errors name it. The extension is lowercased before matching
// the format constraint; an empty extension means "sh" (write the
// script). An extension that does not match the constraint is an
// error, since the user asked for a file we cannot classify.
func resolveFormat(opts *Options) error {
	if opts.Format != "" || opts.Output == "" {
		return nil
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(opts.Output), "."))
	if ext == "" {
		opts.FormatFlag = "-o"
		opts.Format = "sh"
		return nil
	}
	if !formatRe.MatchString(ext) {
		return parseErrf("cannot infer output format from -o, use -f")
	}
	opts.FormatFlag = "-o"
	opts.Format = ext
	return nil
}

// checkFormatConflicts rejects flags that assume execution once a
// format is selected; write mode never executes. -c is allowed only
// for "sh". Errors name opts.FormatFlag so they point at what the
// user typed.
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
		{"-x", opts.Trace},
		{"-b", opts.Buffered},
		{"-s", opts.Sandbox},
		{"-n", opts.Network},
		{"-r", len(opts.Readable) > 0},
		{"-w", len(opts.Writable) > 0},
	} {
		if bad.set {
			return parseErrf("cannot use %s with %s", bad.flag, opts.FormatFlag)
		}
	}
	if opts.Format != "sh" && len(opts.Commands) > 0 {
		return parseErrf("cannot use -c with %s", opts.FormatFlag)
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
