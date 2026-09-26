# Preface

This document records the design of the prompt sent to the model: the
SCRIPT/ERROR reply protocol, the system and user message grammar, the
argument sanitization convention, and the decisions behind them. The
prompt is a contract with an external system — changing any rule
changes what the model does — so the rationale lives here rather than
in code comments.

Read the top-level `.kb/agents.md` file before continuing below.


# Overview

`internal/prompt` builds exactly two messages: a system message that
establishes the rules and the command surface, and a user message that
carries the request and its data. Everything the model learns about the
task comes from these messages — it has no tool calls, no multi-turn
conversation, and no local access. The model answers in a rigid
SCRIPT/ERROR format that `engine.parseReply` decodes; the format is
defined here and only here.


# Important

- The reply protocol (SCRIPT/ERROR tags) is a private contract between
  the prompt and the parser. It is deliberately NOT documented in the
  `Generate`/`parseReply` doc comments — an implementer changing one
  side must consult this document, not just the code next door.
- The prompt never mentions grants, paths on disk, or sandbox state.
  Listing readable/writable paths was rejected: it would bias the model
  toward touching paths it would otherwise ignore (an "allow /etc"
  nudges the model to read /etc even when the request never needed it).
- Arguments get no interpretation from the prompt: they are data,
  listed verbatim (modulo sanitization). Their meaning is defined by
  the request and the model, not by the filesystem.
- The system prompt's wording is pinned by `TestSystemPromptPinned`
  (key rules asserted by substring). Changes must be deliberate.
- The applet list is probed from the installed busybox build
  (`busybox --list`) and rendered into the system message, so the
  model's known command surface always matches the execution
  environment. See `.kb/busybox.md`.


# Architecture

## Message split

The system message carries everything stable across requests: rules,
allowed commands (`-c` names, then the applet list), and the command
references. The user message carries everything request-specific: the
request text and the argument data. The split gives tests a clean
seam: system-message assertions never interact with request content.

## The SCRIPT/ERROR protocol

The system prompt instructs the model, with fenced examples:

1. Unsatisfiable request → output ONLY an ERROR line with a reason:
   `ERROR cannot move a file to itself: /file/path`.
2. Satisfiable request → output ONLY a SCRIPT line followed by the
   script itself, no chat, no code block.
3. One chance: solve it with SCRIPT or fail with ERROR. No retries,
   no clarifying questions — this is a one-shot tool by design.
4. Use ONLY the explicitly allowed command line tools.
5. No path wildcards unless unequivocally sure only the requested
   paths are affected (safety rule: wildcards bypass the explicit
   grant model).
6. Solve simply, cleanly, readably — the script is human-reviewed
   before execution, so legibility is a functional requirement, not
   style.

The rules were authored in a single revision of the system prompt;
rules 1–3 define the reply protocol (the engine contract, see
`.kb/engine.md`), rule 4 matches the ALLOWED COMMANDS surface, and
rules 5–6 constrain what the model writes. No per-rule rationale was
recorded beyond what the rules themselves say.

## Section grammar

System message, in order:

- The rules text.
- `## ALLOWED COMMANDS` — the `-c` command names on one line, then
  the busybox applets on the next. One section, always present (the
  applets are always non-empty), because the applet list is the
  default command surface even with no `-c`.
- `## REFERENCES` — only when at least one `-c` command has captured
  `--help` output; one `### <name> --help` subsection per command,
  in flag order, help verbatim. A single REFERENCES section holds
  all subsections (an early implementation emitted one header per
  command — fixed after review). Commands without `--help` support
  are still allowed (they appear in ALLOWED COMMANDS) but get no
  subsection; they are not crippled, they simply have no reference.

User message, in order:

- `## REQUEST` — the request text verbatim, multi-line supported
  (joined from stdin with newlines when the request came via `-`).
- `## REQUEST DATA` — only when arguments exist; a fenced block with
  one argument per line, then the two contract notes (see
  sanitization below).

## Argument sanitization

Arguments may contain bytes the model should not see raw: newlines
would break the one-argument-per-line block format, and other
unprintables are noise at best. Sanitization replaces each newline,
carriage return, or unprintable rune with the Unicode replacement
character (U+FFFD, rendered as `<?>` in some fonts).

The crucial property — and why the prompt carries the two notes —
is that sanitization is lossy *for the model but not for the script*:
the real, unmodified strings are always available to the script in
`"$@"`. The prompt says so explicitly, both to stop the model from
guessing at the replaced bytes and to steer it toward `"$@"` when
precision matters. Printable non-ASCII (e.g. accented text) passes
through untouched.

An earlier design used `=`/`!` line prefixes to mark precise vs
sanitized lines; it was replaced by this simpler always-sanitize form
with the explanatory note, which conveys the same information without
a per-line protocol the model must learn.

## What is deliberately absent

- **Grants and paths on disk**: listing them would bias the model
  toward touching paths it would otherwise ignore; arguments are the
data.

## Fenced examples and the backtick problem

The system prompt is a Go raw string, but its examples show markdown
fences (the model is *told* not to use code blocks, yet the examples
themselves are fenced for readability). A raw string cannot contain
backticks, so the fences are spliced in via a small `fence` const
concatenation. This is a mechanical quirk of the implementation, worth
knowing before editing the const: the backticks you see in tests come
from that splice, not from the raw literal.

## Reply parsing contract (consumer side)

`engine.parseReply` decodes the model's answer under rules decided
jointly with this prompt; the full state machine and its rationale
(builtins-vs-applets interplay aside) are documented in
`.kb/engine.md` with the parser. From the prompt's perspective the
contract is simply: the reply must contain one SCRIPT or ERROR tag on
a line of its own; everything else is tolerated per the parser's
fence/chatter rules. Changing the prompt's examples changes what the
parser will realistically receive — treat the two as one system.
