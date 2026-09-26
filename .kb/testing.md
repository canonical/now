# Preface

This document records the project's testing philosophy and the specific
nudges that shaped it — the repeated corrections and settled
preferences that a follow-up session should start primed with, rather
than rediscovering by being told again.

Read the top-level `.kb/agents.md` file before continuing below.


# Overview

The suite is stdlib-only, black-box by default, and behavior-pinning:
tests exist to make decided semantics hard to "simplify" away. The
infrastructure — fake LLM, fake bwrap, fake commands on `$PATH`, temp
setup files — is shared across packages and each piece exists because
a real testing problem demanded it.


# Important

These are the nudges — corrections made during development that should
not need repeating:

- **Assert complete values, not fragments.** When a test's purpose is
  to verify a rendered output, assert the whole thing exactly
  (e.g. `TestBuildReferencesSection` compares the full REFERENCES
  section tail, `TestRunErrorReply` compares the entire error string).
  Substring `Contains` assertions were repeatedly caught papering over
  real bugs — including one where a per-command `## REFERENCES`
  header passed a substring check while the actual output was wrong.
  Use `Contains` for presence checks; use full equality for
  correctness checks.
- **Check returned errors from test scaffolding.** `calls, _ :=
  os.ReadFile(...)` was called out: discarding the read error turned a
  missing calls file into a misleading invocation-count failure. Test
  helpers get the same error discipline as production code.
- **Black-box, but not religiously.** Test files declare
  `package <pkg>_test` and import the package under test — except
  `cmd/now`, whose tests are `package main` to reach the unexported
  cycle plumbing. The rule is: prefer black-box; when an unexported
  seam genuinely needs testing, an internal test file in the same
  directory is the accepted exception (and was explicitly chosen over
  exporting a symbol just for tests).
- **One behavior per test, named for the behavior.** When a rule
  inverted during development (e.g. fences-inside-body becoming legal
  content), the *old* test was deleted with the behavior, not kept and
  inverted in place where its name would lie.
- **Environment-dependent tests must skip, not silently pass or
  mislead.** Two kinds exist:
  - `requireBwrap` — the whole sandbox suite skips without bwrap.
  - The no-terminal approval tests skip when `/dev/tty` opens
    (devcontainers have a tty; the test would hang reading it). The
    skip reason string says why.
- **Never run the real `now` binary with real requests to test it.**
  Established after an incident: an assistant reproduced a user's
  failure by invoking `./now` live, which sends a real model request
  and attempts to execute the generated script against the
  filesystem. Debug via the test suite, fake servers, or code reading.
  The tool executes model-generated shell scripts; treating it as a
  casual debug helper is precisely the failure mode it exists to
  prevent.


# Architecture

## Behavior-pinning as a design principle

The AGENTS.md rule ("pin semantics with tests") has concrete history:
multiple parser rules (tags-inside-body, fence placement, label forms)
and pipeline invariants (zero API requests on confinement failure)
were each decided once through discussion, then immediately pinned.
The pattern: **when a subtle rule is settled after back-and-forth,
write the test before moving on** — the next session will not
remember the discussion, only the test.

## The fake LLM (internal/api/completions/fake.go)

An in-process OpenAI-compatible server answering
`POST /v1/chat/completions` with a canned reply, recording every
request body. Beyond the obvious success-path tests, the recording
serves two specific assertions developed during debugging:

- **Shape assertions**: the request carries the right model name and
  messages (this caught `prompt.Message` lacking JSON tags — the
  client was sending `"Content"`/`"Role"` field names, which real APIs
  would have rejected; only a request-capturing fake could see it).
- **Zero-request ordering assertions**: `TestRunSandboxFailsBeforeAPICall`
  counts recorded requests to prove the probe precedes the model call.

The fake retains `EnableRequestLog` (append request bodies to a file,
labeled) — generic conversation recording, kept because it costs
nothing and serves debugging of test conversations.

`SetReply` allows mid-test reply changes; `Requests()` returns
snapshots (copies, not live slices) so assertions on one snapshot
cannot be invalidated by later requests.

## The fake bwrap (internal/sandbox tests)

A shell script installed as `bwrap` on a prepended `$PATH`, recording
invocations to a `calls` file, with three behaviors selected by env
var (`NOW_FAKE=ok|denied|broken`). Exists because exactly one of the
two proc forms (fresh `--proc` vs host `/proc` ro-bind) is reachable
in any given environment — real-bwrap tests can only ever cover one
side. The fake makes the proc matrix deterministic everywhere.

Hard-won details recorded here so they are not re-broken:

- bwrap's real proc failure message contains an apostrophe
  (`Can't mount proc...`); embedding it in the fake via Go string →
  shell quoting produced first a literal `\x27` and then a message
  missing its `t`. The messages are now delivered via `NOW_FAKE_*`
  env vars — keep it that way.
- The fake must run the command after `--` for real (`while ... exec
  "$@"`) so success-path tests still exercise the child.
- Assertion discipline on `calls`: read with error check; the last
  recorded line is the interesting one; assert both what IS present
  (`--proc /proc`) and what is NOT (`--ro-bind /proc /proc` must be
  absent in the fresh-form test).

## Fake commands on `$PATH`

`withFakeCommands(t, names...)` writes tiny scripts into a temp dir
and prepends it to `$PATH`; `withNoHelpCommands` the exit-1 variant
for help-less command coverage. Used for `-c` parsing (captured help
assertions), the alias prelude, and the shadowing tests. The
applet-shadowing tests (`TestRunAliasesAppletShadowingCommand`,
`TestRunAppletWinsWithoutAlias`) are a matched pair: the second pins
the *unfixed* baseline (busybox runs its applet despite `$PATH`) —
the behavior the prelude exists to override; without it, a busybox
build dropping the standalone-shell preference would silently
invalidate the prelude's reason.

## End-to-end via temp setup files

The cycle tests (`cli/run_test.go`) do not stub configuration: they
write a real `$HOME/.now`-format file to a temp path and pass it as
`RunOptions.SetupPath`, pointing `api-url` at the fake server. This
exercises real config loading — a stub would hide format drift.
`SetupPath` exists as a public seam specifically for this.

## What is deliberately not tested

- True interactive terminal approval (a pty harness) — judged not
  worth a dependency for the ENTER path; the `TTY io.Reader` seam
  covers the logic, and the environment-skip covers the rest.
- Exact model wording of the system prompt beyond key substrings —
  the full const is pinned by `TestSystemPromptPinned` on rules that
  matter; churning prose is not asserted.
