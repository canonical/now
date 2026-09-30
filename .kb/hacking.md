# Preface

This document records the coding and testing conventions for
contributing to **now** — the per-change rules an agent must follow
when writing code, tests, and messages. The `.kb/` documents record
why the system is shaped the way it is; this one records how to work
in it cleanly.

Read the top-level `.kb/agents.md` file before continuing below.


# Overview

The conventions below were settled during the phased build of the
project and are enforced by review, not tooling. They cover code
structure (`main` dispatch, naming, literals), user-facing message
phrasing, and the testing discipline (black-box tests, the
`assertEqual` helper, pinning decided semantics). The AGENTS.md file
at the repository root carries the project summary and the workflow
rules; this document carries the conventions themselves.


# Important

- **`main()` only dispatches.** Use the `run() error` pattern: `main`
  calls `run` and handles the error/exit code; all real work returns
  errors properly instead of printing and exiting mid-flow.
- **Variable naming:** use `opts` for `*Options` values, not `o` or
  `a`.
- **Long literal texts** (usage, prompts) are private top-level
  `const`s with lowercase identifiers, the content flush at the first
  column, and the closing backtick on its own line. Keep the
  *displayed* text's own capitalization independent of the
  identifier's.
- **Help/usage output:** print the full usage text only when the user
  explicitly asks for it (`--help`) or gives no arguments at all.
  Every other error gets a concise one-line message; don't append
  usage to it.
- **Terminology:** the user-facing request is a "<request>", not a
  "human query". Code identifiers follow (`Request`, not `Query`).
  When the user corrects terminology, update docs, messages, and
  identifiers together.
- **Error message phrasing:** start with "cannot" — not "unable to",
  "can't", "can not", "failed to", or "<verb>ing" prefixes ("opening
  file", "reading config"). E.g. `cannot open ~/.now: ...`, not
  `opening ~/.now: ...` or `failed to open ~/.now: ...`.
- **Don't over-document.** Drop comments that merely restate code in
  English. Keep comments that add information the code doesn't show
  (intent, non-obvious rules, cross-references).
- **Prefer small helpers** to deduplicate repeated logic (e.g.
  `addWith` for the `-w`/`-w=` flag forms).


# Architecture

## Testing conventions

- **Black-box tests:** test files live next to the code but declare
  `package <pkg>_test` (e.g. `internal/cli/cli_test.go` is `package
  cli_test`) and import the package under test. Do not create separate
  test directories. The accepted exception is `cmd/now`, whose tests
  are `package main` to reach the unexported dispatcher plumbing.
- **`assertEqual` helper:** wrap `reflect.DeepEqual` + `t.Errorf` as
  a generic helper:

  ```go
  func assertEqual[T any](t *testing.T, label string, got, want T)
  ```

  Use it instead of raw DeepEqual checks. Define it in the package's
  test file; don't create a separate file just for it.
- **Test behavior, not internals:** when a private function's
  behavior can be observed through the public API, test it that way.
  Prefer changing the test approach over exposing internals just for
  tests.
- **Pin semantics with tests:** when a subtle rule is decided (e.g.
  where flags are recognized, the stdin `-` placeholder rules), write
  a test that would fail if someone "simplified" it away. The
  behavior-pinning history and the fake infrastructure (LLM, bwrap,
  commands on `$PATH`) are documented in `.kb/testing.md`.
- **Parser bookkeeping stays out of `Options`:** the struct is
  intentionally minimal; stdin is handled inline during parsing, not
  via extra fields.
- **Always inject the TTY in tests that reach approval.** Any test
  whose execution path calls `Approve` must pass `RunOptions.TTY` (or
  `ApprovalOptions.TTY`) with a reader delivering a decision. Without
  it, `Approve` opens the real `/dev/tty` and blocks forever waiting
  for input — the test hangs instead of failing. This bit twice: once
  with an approval test, once with an abort test that reused the `e2e`
  helper (which has no TTY injection). The `e2e` helper covers
  `-y`/`-q` paths precisely because those never read the tty;
  interactive paths need their own setup with an injected one.

## CLI semantics

These are pinned by tests — don't change without updating them. The
design history is in `.kb/cli.md`.

- Flags are only recognized **before** the request; after it,
  anything is a path.
- **Stdin:** exactly one `-` placeholder is allowed in the whole CLI,
  either as the request or as a path — not both. As the request, all
  stdin lines are joined with newlines (multi-line requests are
  supported; empty stdin is an error). As a path, all stdin lines are
  injected at that position. Empty lines are dropped.
- `-c` takes a comma-separated command list; empty names are an
  error. `-r`/`-w` take paths that must exist.

## Workflow

- Validate every change with `go vet ./... && go test ./...` before
  considering it done.
- Keep `README.md` in sync with behavior changes.
- When touching CLI flags — adding/changing/removing — update
  both the usage message in `internal/cli/options.go` and the respective
  code blocks in `README.md` in the same change. All these lists and the
  parser must not drift apart.
- `README.md` has **two** usage excerpts, kept in sync with
  `internal/cli/options.go` but with different scopes — do not collapse
  them:
  - The `## Usage` block mirrors the `usage` const's flag list
    (the `Options` / `Running control` / `Sandbox mode` / `Output mode`
    sections and their alignment/placeholders), but **compact**: drop
    the const's prose description and example, keep only the flag
    listing. Match the const's section headers and `<cmd>`/`<path>`/
    `<format>` placeholders verbatim.
  - The `## Sandboxing and isolation` section carries a **scoped
    subset** (only the sandbox flags) as a "relevant parameters"
    block. Keep it to that subset — do not expand it to the full list,
    and keep its simpler `path` form (no `<>` placeholders), matching
    the surrounding section's focus.