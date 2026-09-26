# Preface

This document records the design of the model API layer: the
completions client, the fake server, the configuration keys, and the
injected-dispatch seam that decouples the engine from any particular
backend. The emphasis is on decisions and their histories rather
than the code, which is self-documenting.

Read the top-level `.kb/agents.md` file before continuing below.


# Overview

`internal/api/completions` is the only API backend: an
OpenAI-compatible chat-completions client, stdlib-only, speaking to
whatever `api-url` points at — a local server (`http://127.0.0.1:11434`
style), a proxy, or a hosted API. The package deliberately knows
nothing about **now**: it sends `prompt.Message` values and returns
the reply string. Selection between present and future backends is a
configuration concern (`api-type`), dispatched by the CLI.


# Important

- **The `/v1` prefix is appended by the client, not the user.** The
  config's `api-url` is a bare base URL (`http://127.0.0.1:11434`,
  trailing slash tolerated — trimmed); the client builds
  `<base>/v1/chat/completions`. This was changed after the initial
  version required the user to know about `/v1`, which was judged
  wrong: local servers conventionally present themselves without it,
  and the prefix is an artifact of this specific API shape.
- **The client takes `setup.Options` directly**, not a package-local
  struct. An earlier `completions.Options` duplicated the fields; the
  rename made the duplication pointless and the signature now says
  plainly what a backend needs (URL, key, model) — the same struct
  every `api-type` backend will consume.
- **The engine does not import this package.** `engine.GenerateOptions.
  Complete func(ctx, messages) (string, error)` is injected by the
  CLI, which closes over the loaded setup options. This started as
  decoupling-for-testability (the engine tests use the fake without
  importing the real client) and became the extension point: a new
  `api-type` backend is a new function the CLI can inject, and the
  engine never changes. See `.kb/engine.md`.
- **Config keys and defaults** (validated at load):
  - `api-url` — required, no default; missing is a load error.
  - `api-key` — optional; sent as `Authorization: Bearer <key>` only
    when non-empty (local servers often need none).
  - `api-model` — defaults to `default`, chosen to work with servers
    that ignore or resolve the model name themselves (llama.cpp ignores
    it; Ollama requires a real tag like `llama3.1:8b`).
  - `api-type` — defaults to `completions-v1`; any other value is a
    load error (`unsupported api-type`). Only this backend exists;
    the key exists to make the extension seam real rather than
    hypothetical, and its validation is where backend selection will
    dispatch.
- **Error surface**: transport failures (`cannot reach <url>`), HTTP
  non-200 (status plus response body — the body carries the server's
  own error explanation, e.g. an unknown-model message from Ollama),
  malformed JSON (`cannot parse response`), empty `choices`
  (`response has no choices`). All "cannot"-phrased per project
  convention. The reply content is returned verbatim — protocol
  parsing (SCRIPT/ERROR) is the engine's, never the client's.


# Architecture

## Wire shape, and the JSON-tag lesson

The request is `{model, messages}` with `prompt.Message` carrying
`role`/`content` tags. The tags were missing initially and only the
fake server caught it: the client was emitting Go field names
(`"Content"`, `"Role"`), which every real API would reject. The lesson
is recorded in `.kb/testing.md` — request-capturing fakes see what
subprocess-free unit tests cannot — and is the reason the fake
asserts request *shape* (model name, message contents), not just
that a request happened.

The response is decoded minimally: only `choices[0].message.content`
is consumed. Nothing else of the completion shape is modeled — no
usage stats, no finish reasons — so schema drift in ignored fields
cannot break the client.

## The fake server (fake.go)

`FakeLLM` binds a loopback port, answers `POST /v1/chat/completions`
with a canned reply, and records every request body in arrival order:

- `NewFakeLLM(reply)`, `SetReply` for mid-test changes.
- `Requests()` / `LastRequest()` return copies, so a held snapshot
  cannot be invalidated by later traffic.
- `EnableRequestLog(path, label)` appends bodies to a file — generic
  conversation recording; kept as a zero-cost introspection tool for
  debugging test conversations. It has no production use.
- Method guards (405 on non-POST), body decode guards (400 on
  non-JSON, not recorded), Stop idempotency, post-Stop refusal to
  connect — all pinned by its own test file.

The fake answers `GET /` with nothing useful — a 404 — matching real
servers closely enough that URL-shape mistakes surface as connection
or status errors in tests.

## Naming history worth knowing

The package was born as `internal/api/chat`, moved to
`internal/api/completions` on the grounds that the *API kind* is the
stable name and `chat` described the protocol shape; the config key
followed as `api-type=completions-v1`. Within the package, `Run`
grew into `Args` then briefly `CmdArgs`/`BwrapArgs` during the sandbox
work — that churn belongs to `.kb/sandbox.md`; the API layer's own
naming has been stable since the move.

## What is not built yet

- **Retries**: must be implemented. Transient server failures
  currently surface directly to the user.
- **Streaming**: the reply arrives whole; the SCRIPT/ERROR protocol
  is one-shot by design.
- **Multiple choices / sampling parameters**: `choices[0]` and server
  defaults.
- **API-version negotiation**: `/v1` is hardcoded into the URL
  construction. When a new API shape arrives, it is a new
  `api-type` backend.
