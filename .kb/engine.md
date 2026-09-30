# Preface

This document records the design of `internal/engine` — the Generate and
Run phases — including the SCRIPT/ERROR reply parser and the alias
prelude, with the reasoning that shaped them. The parser's rules in
particular were decided across many rounds of discussion; this document
preserves why they are what they are.

Read the top-level `.kb/agents.md` file before continuing below.


# Overview

The engine is the two-phase core of **now**: `Generate` turns a request
into a script (prompt → model API → reply parsing), and `Run` executes
an approved script under busybox, confined when requested. The split is
deliberate: approval lives entirely with the CLI between the two
phases — the engine never asks questions, never decides whether a
script should run, and never reads the terminal. Skipping execution is
simply not calling `Run`.


# Important

- **The engine owns no policy.** Whether to run a generated script, how
  to show it, and whether to ask a human are CLI concerns. The engine's
  contract ends at producing a script and executing one it is handed.
  This was an explicit design decision after an earlier `runner`
  proposal put approval hooks inside the package; it was rejected
  because approval I/O and script-execution I/O are different concerns
  that would both end up threaded through one Options struct.
- **Probing precedes the model.** Callers must resolve busybox and,
  when confining, probe the sandbox before calling `Generate`. Both
  facts are stated in `RunOptions` doc comments and pinned by
  `TestRunSandboxFailsBeforeAPICall` (zero API requests when
  confinement is broken). The rationale: a model call costs money and
  the human review costs attention — neither should be spent on a
  script that cannot run.
- **The model dispatch is injected, not imported.** `GenerateOptions.
  Complete func(ctx, messages)` decouples the engine from the
  completions package. This started as a refactor to keep the engine
  testable with the fake API, and became the extension point for
  other `api-type` backends (the config already carries `api-type`;
  only `completions-v1` exists today).
- Reply-parsing failures are errors with model-facing text
  (`cannot perform the request: <reason>`); transport failures pass
  through unchanged. The end-to-end test asserts the *complete* error
  string so the model's reason survives the whole pipeline verbatim.
- **`GenerateOptions.Format` selects the reply protocol.** Empty or
  `"sh"` is script mode (the SCRIPT/ERROR tags); any other value is
  output mode (the OUTPUT/ERROR tags, see `.kb/prompt.md`). The field
  threads through `prompt.Build` and into `parseReply`, which is
  mode-aware: a SCRIPT tag in output mode (or an OUTPUT tag in script
  mode) is unexpected output, not a success, so the two contracts stay
  distinct rather than one parser accepting all markers leniently.
- **`GenerateOptions.Output` selects the arg framing for scripts.**
  It is meaningful only in script mode (Format empty or `"sh"`): when
  false the prompt frames args as `"$@"` with `$1`/`$2` indexing (the
  script will execute and read them there); when true the prompt
  frames args as plain `## DATA` (the script is dumped, never runs, so
  `"$@"` is meaningless). Format mode ignores it. The field threads to
  `prompt.BuildOptions.Output`; see `.kb/prompt.md`. `parseReply` does
  not branch on it — the reply protocol is chosen by `Format` alone.


# Architecture

## The reply parser (parseReply)

The model is instructed (see `.kb/prompt.md`) to answer with a SCRIPT
or ERROR tag (script mode) or an OUTPUT or ERROR tag (output mode).
Real models add chatter and markdown fences; the parser tolerates them
under rules that took several iterations to settle:

- **Chatter before the tag is ignored.** "Sure, here it is:" etc.
- **The tag must be a line of its own.** SCRIPT or `ERROR <reason>`.
  An ERROR with an empty reason is rejected as unexpected output.
- **After the SCRIPT tag, later SCRIPT/ERROR lines are ordinary script
  content** — a script may legitimately echo "SCRIPT" or print
  "ERROR: disk full". Only the first tag, before the body started, is
  a control line.
- **Without fences, the script runs to the end of the reply.** Trailing
  chatter after an unfenced script becomes script content — accepted
  trade-off: the prompt forbids chatter, so a well-behaved model ends
  the reply, and cutting at unknown content would risk truncating real
  script lines.
- **Fence placement rules** (the subtle part):
  - A fence opened right before or right after the SCRIPT tag wraps
    the script body; the payload then ends at the **last** closing
    fence seen (fenceClose records the high-water mark, so a script
    containing its own fenced heredocs is not truncated at the first).
  - A fence opened and closed **before** the tag is simply unfenced —
    an unrelated code block in the chatter. The parser resets and
    continues as if it had just started.
  - A SCRIPT tag found **inside** a fence that already has content
    before the tag is ignored (fenceDirty): a tag inside an unrelated
    code block makes no sense.
  - Fences opened **inside** the script body, with no wrapper fence
    around the tag, are ordinary script content (a heredoc, say) and
    do not end anything.
- **An empty script is an error** distinct from unexpected output:
  `cannot create script: model returned an empty script`.

History worth knowing: the first implementation was prefix string
cutting, then a fence-unwrap helper, then a line-by-line state machine
that was rewritten twice — once for fence-before-vs-after-the-tag
semantics, once to make tags-inside-body legal content. The surviving
shape is a loop over lines tracking four booleans/an int (script
started, fence open, fence dirty, last close position). Each revision
was driven by design review of the rules above, not by observed model
output; the tests pin the rules as decided.

## The alias prelude

`Run` prepends `alias <name>=<absolute-path>` for every allowed command
before the script text. Two independent reasons converged on
alias-**all**:

- **Shadowing commands** (`-c tar` vs the busybox tar applet) must be
  aliased or busybox's standalone-shell applet preference silently
  runs the applet instead of the user's resolved binary — this holds
  unconfined too, it is a shell property, not a sandbox one.
- **Inside the sandbox** no `$PATH` directory is bound, so
  non-shadowing commands would not resolve by name at all. Aliases to
  absolute paths give them a resolution path.

Uniformity is the design value: every `-c` command runs exactly the
binary resolved at parse time, whether shadowing or not, confined or
not. An earlier conditional form (`IsBusyboxApplet` check, alias only
the shadowing ones) was replaced; the check and its hardcoded const
were later deleted entirely when applets became probe-driven. The
interplay between these two concerns (applet preference vs `$PATH`
resolution) is documented in `.kb/busybox.md`; the trap is treating
them as one problem.

## Execution mechanics

- The script travels on **stdin** (`busybox sh -t? -e -s`) — no
  temp files, no artifacts — and `opts.Args` are delivered as
  `"$@"`. A `--` separator precedes the arguments when any exist:
  without it, busybox parses everything after `-s` as shell options,
  so an argument starting with `-` (say `-r foo`) kills the script
  with `sh: illegal option -r` before it runs.
- fd 0 belongs to the script source; the user's stdin — the data a
  script may read — rides on **fd 9**. `Run` wraps the script in a
  group ending `} <&9 9<&-`, which attaches the user's stream as
  the script's fd 0 and closes the spare descriptor, so a script
  that reads (`read`, `python3 -`, `curl` bodies) receives the
  user's data, never its own source. The CLI tees the user's stdin
  through parsing (`cli.Run`), so data consumed by the `-`
  placeholder is nonetheless delivered to the script.
- fd 9 is the highest single-digit descriptor: the redirection
  works in any POSIX shell (multi-digit forms like `<&63` are an
  extension busybox ash supports but POSIX does not require), and
  everything below — fds 3-8 — stays free for the script's own
  `exec 3<...` style usage. `exec.Cmd.ExtraFiles` numbers entries
  from fd 3, so `Run` pads with six nil placeholders to land the
  stream on 9; the ceiling for any such scheme is the soft
  `RLIMIT_NOFILE`.
- The wrapping group is required rather than stylistic: ash parses
  a compound statement completely before executing any of it, so
  the fd 0 swap takes effect only after the entire script has been
  parsed, and the shell can never fall back to reading user data as
  script source. A bare `exec 0<&9` prelude has no such guarantee —
  the shell reads the script from fd 0 in buffered chunks, and once
  its buffer drains past the swap it would parse the user's data as
  commands.
- Overloading fd 0 to mean both script and data is the trap this
  split exists to avoid: a `read` would consume the script's own
  remaining source, or hit EOF and fail under `-e`.
- Non-file readers (test fakes, `strings.Reader`) are bridged
  through an `os.Pipe` by a copier goroutine so fd 9 is always a
  real descriptor — `exec.Cmd.ExtraFiles` carries only `*os.File`.
  A nil stdin becomes `/dev/null`, so a script read gets honest
  EOF.
- `exec.CommandContext` throughout, so a canceled context (CTRL-C
  via the CLI's `signal.NotifyContext`) kills the process tree,
  including under bwrap, which inherits extra descriptors with no
  special flag.
- `RunOptions.Stdout`/`Stderr` are the script's own streams; the
  engine never writes prompts or review text to them (that is the
  CLI's business, on different writers).
- Under `SandboxOn`, the busybox path and shell arguments are handed
  to `sandbox.Args`, which returns the complete bwrap argument vector
  (see `.kb/sandbox.md` for the confinement side).
