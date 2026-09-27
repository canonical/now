# Preface

This document records the design of `internal/cli` and `cmd/now` — the
flag grammar, the approval flow, the stream discipline, and the full
cycle that threads every subsystem together. The emphasis is on
decisions and discussions that the code cannot show.

Read the top-level `.kb/agents.md` file before continuing below.


# Overview

The CLI is the orchestrator and the only policy-maker in the project:
it parses the command line, loads configuration, resolves busybox,
probes confinement, generates the script, **owns the approval step**,
and executes. Every other package is mechanical — the division of labor
is deliberate (see `.kb/engine.md` for the engine side of that split).
`cli.Run(RunOptions)` performs the full cycle; `cmd/now/main.go` is a
four-line dispatcher that hands it the real streams.


# Important

- **stdout belongs to the script; stderr belongs to now.** The
  generated script's output goes to stdout so it can be piped; the
  script-for-review, approval prompts, and all errors go to stderr.
  This was decided after an earlier design printed the script to
  stdout and switched behavior on whether stdout was a terminal —
  rejected as embedding CLI policy in the wrong place and for
  breaking the pipe use case (script output had to be separated from
  the review text).
- **Approval reads /dev/tty, never stdin.** stdin is data (the `-`
  placeholder consumes it); the approval question goes to the
  controlling terminal so both can coexist. This fixed a real bug:
  `cat file | now "explain this" -` drained stdin during parsing and
  the approval read then saw EOF — which, worse, silently read as an
  empty line and auto-approved.
- **No terminal and no `-y`/`-q` is an error, not a skip.** "We had to
  approve but cannot" — exit 1 with `cannot ask for approval without
  a terminal`, rather than guessing the user's intent.
- **CTRL-C cancels cleanly in any phase**: `signal.NotifyContext` at
  `cli.Run` entry; the context threads through the API call
  (`http.NewRequestWithContext`), the approval wait (`select` on
  `ctx.Done()`), and execution (`exec.CommandContext`). Before this,
  only the approval phase handled the signal, and the semantics
  changed per phase — documented as the "Signal handling" TODO item
  and resolved by threading context everywhere.
- **Fail-fast ordering, all before the model call**: parse errors,
  missing commands (`-c`), missing grant paths (`-r`/`-w`), config
  load, busybox resolution, sandbox probe. `TestRunSandboxFailsBefore-
  APICall` pins the strongest form: broken confinement → zero API
  requests. The rationale is constant: the model call costs money and
  the human review costs attention; neither is spent on an invocation
  that cannot run.


# Architecture

## The full cycle (cli.Run)

Order matters and each step's position is a decision:

1. `signal.NotifyContext` — the root context, from the entry point.
2. `Parse(argv, stdin)` — flags and the request; `-h`/`--help` prints
   usage and exits zero *here*, not in main.
3. Setup load — `$HOME/.now` or the injected `SetupPath` (exists for
   tests, which point it at a temp file with a fake API URL).
4. `busybox.Probe` — resolves path + applets; both the prompt (applet
   list) and the sandbox (binary path) consume the result, so it
   precedes both.
5. Grant assembly + `sandbox.Probe` when confining — including adding
   the `-c` command paths to `Readable` (the `-c` implies `-r` rule;
   note it applies *only when already confining* — `-c` alone never
   turns confinement on).
6. `engine.Generate` — with `Complete` closing over the loaded setup
   options; this closure is the only place the completions package
   enters the cycle, keeping the engine decoupled (see
   `.kb/engine.md`).
7. `Approve` — the CLI's own policy domain (below).
8. `engine.Run` — only if approved; **not calling Run is the skip
   path**, there is no "don't run" flag to thread through.

## Flag grammar

- Flags are recognized **only before the request**; everything after
  it is an argument. Pinned by tests (e.g. `-w` after the request is a
  plain argument).
- `-y` approve, `-q` approve-and-hide, `-t` trace, `-c cmd,...`,
  `-r path`, `-w path`, `-n` network, `-s` sandbox. `=`-forms work
  for value-taking flags. Grants repeat and accumulate.
- Exactly one `-` placeholder in the whole invocation, either as the
  request (stdin lines joined with newlines; multi-line requests are a
  feature — organizing a request with space is a primary use case,
  hence no line limit) or as an argument position (stdin lines
  injected there). Never both. Empty stdin as the request is an
  error; empty lines are dropped.
- **History of dropped designs** (each was implemented or designed,
  then removed — do not resurrect without discussing):
  - Labeled path sets (`1: ... 2: ...`) — superseded when arguments
    became a flat, meaning-free list.
  - `-x` trace — renamed to `-t` to free `-x` for a *possible future*
    execution-grant flag; bwrap cannot express execute-only binds
    today (see `.kb/sandbox.md`).
  - `-w` commands — renamed to `-c`; `-w` now means writable grant.
  - `-l` local mode (all writable, network off) — deleted as a
    redundant spelling of `-w /`.
  - Usage on error — usage prints only on explicit `--help` or no
    arguments; errors are one line, no appended usage.
- `Options` is intentionally minimal; parser bookkeeping must not
  leak into it (stdin is handled inline during parsing).

## Buffered mode (-b)

`-b` captures output and shows it only when the script fails, so a
successful run stays quiet. The buffer is a single internal
`bytes.Buffer` serving as both the script's stdout and stderr.

The approval output follows the same destination as the script
output — `cli.Run` computes the writers (buffer or real) *before*
calling `Approve`, so the review and the execution share one stream:

- **`-b` alone**: script shown on real stderr, approval read from the
  tty, then execution output buffered.
- **`-by`**: the script review and the `[ ENTER | CTRL-C ]`
  separator go into the buffer too. A successful run produces
  nothing at all; on failure the whole buffer flushes to stderr.
- **`-bq`**: Quiet suppresses the script and the separator; nothing
  is produced anywhere, and the buffer stays empty on success.
- **`-y` (and `-by`)**: the `[ ENTER | CTRL-C ]` line always prints
  to the selected output even though no question is asked — the
  usual separation between the printed script and the output of the
  script. With `-b` it lands in the buffer; with plain `-y` on
  stderr. Quiet is the only mode that suppresses it.

`-b` composes with `-t`: trace output also goes to the buffer and
only appears on failure.

## Approval (Approve)

- `-q` hides the script entirely; otherwise the script prints on
  stderr before asking.
- `-y`/`-q` approve without asking — including when stdout is a pipe.
  (An earlier behavior keyed on stdout-not-a-terminal to *skip*
  execution; that entire notion was removed with the stdout/stderr
  redesign — the terminal check now only decides *whether a question
  can be asked*, and its absence is an error, not a mode.)
- The question is printed to stderr; the answer is read from
  `/dev/tty` (injectable as `ApprovalOptions.TTY` for tests — the
  field exists because tests cannot open the real tty reliably, and
  it doubles as the seam that made interactive-path tests possible).
- ENTER (empty line) approves; any other input cancels; EOF cancels
  — explicitly distinguished from an empty line, because a drained
  tty reading EOF would otherwise auto-approve. The EOF-vs-empty-line
  bug was real and is pinned by `TestApproveEOFCancels`.
- CTRL-C during the wait selects on `ctx.Done()` and cancels — the
  line-reading goroutine is abandoned (bounded leak; the process is
  about to exit or move on).

## Testing design

- The e2e tests run the whole cycle against the fake completions
  server via a temp `SetupPath` — this exercises the real config
  loading, not a stub.
- `cmd/now` tests live in `package main` (not `_test`): they test the
  unexported `run`-era plumbing; the cycle itself moved to
  `cli.Run` where it is testable black-box.
- Environment-dependent paths skip honestly: bwrap unavailability
  (`requireBwrap`), a controlling terminal existing (the no-terminal
  approval tests skip when `/dev/tty` opens — in a devcontainer with a
  tty they cannot run, and would otherwise hang on the real tty).
- The fake-bwrap/fake-LLM/fake-command patterns and the
  zero-API-request ordering assertions are shared infrastructure
  across packages; see also `.kb/busybox.md` (fake busybox probing)
  and `.kb/sandbox.md` (fake bwrap proc-form matrix).
