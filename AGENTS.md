# AGENTS.md

Conventions and quality bar for working on this codebase. Follow these in every change.

## Project

**now** is a single Go binary (module `github.com/niemeyer/now`, stdlib only, no external dependencies) that sends a request to an OpenAI-compatible API to generate a one-shot shell script, shows it for user approval, and runs it confined to the provided paths. See `README.md` for the design and `TODO.md` for the phased implementation plan. A phase is only done when polished: clean error messages, edge cases handled, and tests covering them.

## Code conventions

- **`main()` only dispatches.** Use the `run() error` pattern: `main` calls `run` and handles the error/exit code; all real work returns errors properly instead of printing and exiting mid-flow.
- **Variable naming:** use `opts` for `*Options` values, not `o` or `a`.
- **Long literal texts** (usage, prompts) are private top-level `const`s with lowercase identifiers, the content flush at the first column, and the closing backtick on its own line. Keep the *displayed* text's own capitalization independent of the identifier's.
- **Help/usage output:** print the full usage text only when the user explicitly asks for it (`-h`/`--help`) or gives no arguments at all. Every other error gets a concise one-line message; don't append usage to it.
- **Terminology:** the user-facing request is a "<request>", not a "human query". Code identifiers follow (`Request`, not `Query`). When the user corrects terminology, update docs, messages, and identifiers together.
- **Error message phrasing:** start with "cannot" — not "unable to", "can't", "can not", "failed to", or "<verb>ing" prefixes ("opening file", "reading config"). E.g. `cannot open ~/.now: ...`, not `opening ~/.now: ...` or `failed to open ~/.now: ...`.
- **Don't over-document.** Drop comments that merely restate code in English. Keep comments that add information the code doesn't show (intent, non-obvious rules, cross-references).
- **Prefer small helpers** to deduplicate repeated logic (e.g. `addWith` for the `-w`/`-w=` flag forms).

## Testing conventions

- **Black-box tests:** test files live next to the code but declare `package <pkg>_test` (e.g. `internal/cli/cli_test.go` is `package cli_test`) and import the package under test. Do not create separate test directories.
- **`assertEqual` helper:** wrap `reflect.DeepEqual` + `t.Errorf` as a generic helper:
  ```go
  func assertEqual[T any](t *testing.T, label string, got, want T)
  ```
  Use it instead of raw DeepEqual checks. Define it in the package's test file; don't create a separate file just for it.
- **Test behavior, not internals:** when a private function's behavior can be observed through the public API, test it that way (e.g. the label rules are tested via `Parse`). Prefer changing the test approach over exposing internals just for tests.
- **Pin semantics with tests:** when a subtle rule is decided (e.g. what counts as a label, where flags are recognized), write a test that would fail if someone "simplified" it away.

## CLI semantics (pinned by tests — don't change without updating them)

- Flags (`-w`, `-w=`, `-y`, `-h`, `--help`) are only recognized **before** the request; after it, anything is a path.
- **Stdin:** exactly one `-` placeholder is allowed in the whole CLI, either as the request or as a path — not both. As the request, all stdin lines are joined with newlines (multi-line requests are supported; empty stdin is an error). As a path, all stdin lines are injected at that position. Empty lines are dropped.
- `-w` takes a comma-separated command list; empty names are an error.
- `Options` is intentionally minimal: `{ Request; List; With; Yes }`. Don't add fields for parser bookkeeping — stdin is handled inline during parsing.

## Workflow

- Validate every change with `go vet ./... && go test ./...` before considering it done.
- Keep `README.md` and `TODO.md` in sync with behavior changes; check off TODO items only when the phase meets the polish bar.