# Preface

**now** is a stdlib-only Go CLI (module `github.com/canonical/now`) that
sends a natural-language request to an OpenAI-compatible API, generates
a one-shot shell script, shows it for approval, and runs it under
busybox — optionally confined with bwrap. If your task touches any of
that — parsing, prompts, the API client, execution, or the sandbox —
this file and `.kb/` are relevant; start with `.kb/hacking.md` for
the conventions.

Read the top-level `.kb/agents.md` file before continuing below.


# Directory

- `cmd/now/` - Binary entry point; `main` only dispatches to
  `cli.Run`.
- `internal/cli/` - Flag parsing, setup loading, orchestration of the
  probe → generate → approve → run cycle, approval interaction.
- `internal/prompt/` - System/user message construction and the
  SCRIPT/ERROR reply protocol.
- `internal/api/completions/` - OpenAI-compatible chat completions
  client and the in-process fake server used by tests.
- `internal/engine/` - Reply parsing (`parseReply`) and script
  execution (`Generate`/`Run`), including the alias prelude.
- `internal/setup/` - Configuration file loading (`api-url`,
  `api-key`, `api-model`, `api-type`).
- `internal/busybox/` - busybox resolution and applet probing.
- `internal/sandbox/` - bwrap argument construction, probing of the
  procfs forms, and grant mapping.
- `README.md` - Project design for humans.


# Documents

- `.kb/agents.md` - General rules for the knowledge base reading and writing.
- `.kb/hacking.md` - Coding, testing, and workflow conventions.
- `.kb/cli.md` - CLI surface: flags, stdin rules, approval flow, design history.
- `.kb/prompt.md` - System/user prompt grammar, sanitization, reply protocol.
- `.kb/engine.md` - `Generate`/`Run` split, reply parsing, alias prelude.
- `.kb/api.md` - Completions client, setup options, what is not built yet.
- `.kb/busybox.md` - busybox as the execution environment, applet probing.
- `.kb/sandbox.md` - Grants, bwrap mapping, procfs probe, package boundaries.
- `.kb/testing.md` - Testing discipline, fakes, and the no-live-binary rule.
