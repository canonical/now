# Preface

This document records how **now** uses busybox: why it is the execution
environment, how it is resolved, how its applets are surfaced to the
model, and the shell-behavior quirks that shaped the implementation.
It exists so an agent can pick up this subsystem without rediscovering
the pitfalls.

Read the top-level `.kb/agents.md` file before continuing below.


# Overview

Generated scripts run under **busybox ash** — unconfined when no
sandbox flags are given, wrapped in bwrap otherwise (see
`.kb/sandbox.md`). busybox is a hard requirement, not a preference: the
system prompt promises the model a busybox environment, so running
scripts under any other shell would betray that promise. The
`internal/busybox` package resolves the binary and captures what it
provides, before the model is called.


# Important

- busybox is mandatory. No `sh` fallback exists or should exist; a
  missing busybox is an error at probe time, before any model call.
- `busybox.Probe` is called once per invocation, early in `cli.Run` —
  before setup-independent work and before script generation — because
  both the prompt (applet list) and the sandbox (binary path) consume
  its result.
- The applet list comes from the **installed build's `--list` output**,
  not a hardcoded constant. A hardcoded list was tried first and
  rejected: the installed build is the environment the script runs in,
  and a constant can drift from it.
- Scripts are fed via **stdin** (`busybox sh -s`), not a script file:
  no temp files, no artifacts, and arguments still arrive in `"$@"`
  after `-s`.
- The alias prelude (see _Architecture_) is prepended to every script,
  so `$PATH`-based resolution is never relied upon inside the sandbox.


# Architecture

## The `internal/busybox` package

Single concern: resolve and describe. `Probe(opts Options) (Options,
error)` runs `exec.LookPath("busybox")`, then `busybox --list`, and
returns `Options{Path, Applets}`. Already-filled options short-circuit
(a cheap caching pattern shared with `sandbox.Probe`). Empty `--list`
output is an error — a busybox without applets would break the prompt's
promise.

## Where the results flow

- `cli.Run` probes busybox first, then passes `busybox.Options` into
  both downstream consumers:
- `engine.Generate` → `prompt.Build` receives `Applets` and renders the
  `## ALLOWED COMMANDS` section as the `-c` command names followed by
  the applet list. The model thus knows the exact command surface of
  the environment it is scripting for.
- `engine.Run` uses `Path` as the interpreter — `exec.CommandContext(ctx,
  path, "sh", ...)`, plus `-x` when tracing (`-t`), with the script on
  stdin and `opts.Args` delivered as `"$@"`.

## Applets are not shell builtins

Two distinct mechanisms, easy to conflate:

- **Shell builtins** (`echo`, `cd`, `:`) are in-memory function calls in
  the ash process — no filesystem involved.
- **Applets** (`cat`, `tar`, `ls`, ~300 others) are separate command
  implementations compiled into the busybox binary. ash with
  `FEATURE_SH_STANDALONE` checks its applet table *before* `$PATH`, so
  `tar` runs the busybox applet even when real GNU tar is installed
  and earlier in `$PATH`. Verified empirically; this behavior is
  compile-time, with no runtime switch.

This is why `-c tar` alone does not give the model real tar: the script
would run busybox's tar applet regardless of `$PATH`. The **alias
prelude** in `engine.Run` prepends `alias <name>=<absolute-path>` for
every `-c` command before the script text, uniformly (not only the
shadowing ones) — uniformity means each granted command runs exactly
the binary resolved at parse time.

## The `/proc/self/exe` dependency (NOEXEC applets)

Applets re-exec themselves through `/proc/self/exe` — the NOEXEC subset
needs a clean process state that a fork alone cannot provide. Inside
the sandbox this is why procfs must be available (fresh `--proc` or
host `/proc` ro-bind; the choice is probed, see `.kb/sandbox.md`).
Without procfs, applet dispatch fails with `sh: cat: not found` even
though the busybox binary itself is bound and `echo` (a true builtin)
works — a confusing signature worth recognizing on sight.

Earlier design, rejected: a generated directory of applet symlinks
bound at `/bin` with `PATH=/bin` gives every applet a real `$PATH`
entry and sidesteps procfs entirely — but it creates per-run artifacts
and cleanup machinery, against the project's leanness. The procfs
probe replaced it.

## `-w`/`--help` interplay

External commands (`-c`) may shadow applets (`-c ls` vs the `ls`
applet). Resolution happens at parse time via `$PATH` and captures
`--help` verbatim for the prompt — a shadowing command's help describes
the real binary, which is what the script will actually run thanks to
the alias. busybox's own `--help`-less commands are irrelevant here:
the applet list itself is the reference for the built-in surface, and
the system prompt says to use only the allowed tools.

## Terminology

"Applet" is the term for busybox's compiled-in commands; "builtin" was
used early in the project and renamed everywhere to keep one word for
one meaning. `IsBusyboxApplet` (a map-based membership check over the
applet names) existed for the shadowing conditional in the alias
prelude; the alias-everything decision removed its only caller and it
was deleted along with its hardcoded const when the applet list became
probe-driven. Reviving it against the probed `Applets` slice is
trivial if a parse-time "shadows an applet" hint is ever wanted.
