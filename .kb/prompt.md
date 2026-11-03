# Preface

This document records the design of the prompt sent to the model: the
SCRIPT/ERROR reply protocol, the system and user message grammar, the
argument sanitization convention, and the decisions behind them. The
prompt is a contract with an external system - changing any rule
changes what the model does - so the rationale lives here rather than
in code comments.

`internal/prompt` builds exactly two messages: a system message that
establishes the rules and the command surface, and a user message that
carries the request and its data. Everything the model learns about the
task comes from these messages - it has no tool calls, no multi-turn
conversation, and no local access. The model answers in a rigid
SCRIPT/ERROR format that `engine.parseReply` decodes; the format is
defined here and only here.

IMPORTANT: Read the .kb/agents.md file.


# Laws

## §1 - The reply protocol is a private contract

The SCRIPT/ERROR tags are a private contract between the prompt and
the parser. It is deliberately NOT documented in the
`Generate`/`parseReply` doc comments - an implementer changing one
side must consult this document, not just the code next door.

## §2 - The prompt never mentions grants, paths on disk, or sandbox state

Listing readable/writable paths was rejected: it would bias the model
toward touching paths it would otherwise ignore (an "allow /etc"
nudges the model to read /etc even when the request never needed it).

## §3 - Everything shown to the model is request data

Arguments and attachments are data, listed verbatim (modulo
sanitization) and never interpreted by the prompt. Their meaning is
defined by the request and the model, not by the filesystem. The
exception is the attachment headers themselves: a `-i` header
carries the real path, because the model may tell the script to read
the file - and that is the only place a disk path appears.

## §4 - The model's known command surface matches the runtime

The system prompt lists exactly the commands the script may use:
the `-c` names with their captured `--help`, plus the applets
probed from the installed busybox build (`busybox --list`). The
model must neither invent commands nor guess at one-shot syntax.

## §5 - The model gets one chance

One prompt, one reply, no tool calls, no multi-turn, no local
access. Everything the model needs must be in the two messages;
anything not there does not exist for it. Notes worded for the
model (the sanitization marker, the staleness footers) exist to make
that single shot succeed.


# Concepts

## Message split

The system message carries everything stable across requests: the
rules and the allowed commands (`-c` names, then the applet list).
The user message carries everything request-specific: the
attachments (`-c` help, `-i` files), the request text, and the
argument data. The split gives tests a clean seam: system-message
assertions never interact with request content.

## The SCRIPT/ERROR protocol

The system prompt instructs the model, with fenced examples:

1. Unsatisfiable request → output ONLY an ERROR line with a reason:
   `ERROR cannot move a file to itself: /file/path`.
2. Satisfiable request → output ONLY a SCRIPT line followed by the
   script itself, no chat, no code block.
3. One chance: solve it with SCRIPT or fail with ERROR. No retries,
   no clarifying questions - this is a one-shot tool by design.
4. Use ONLY the explicitly allowed command line tools.
5. No path wildcards unless unequivocally sure only the requested
   paths are affected (safety rule: wildcards bypass the explicit
   grant model).
6. Solve simply, cleanly, readably - the script is human-reviewed
   before execution, so legibility is a functional requirement, not
   style.

The rules were authored in a single revision of the system prompt;
rules 1–3 define the reply protocol (the engine contract, see
`.kb/engine.md`), rule 4 matches the ALLOWED COMMANDS surface, and
rules 5–6 constrain what the model writes. No per-rule rationale was
recorded beyond what the rules themselves say.

## The OUTPUT protocol (format mode)

When `-f` selects a format other than `sh`, `Build` switches to a
separate system prompt (`formatPrompt`) that asks the model to
produce content in the named format rather than a script. The success
delimiters are `---OUTPUT-START---` / `---OUTPUT-END---`; the failure
delimiters are the same `---ERROR-START---` / `---ERROR-END---` as the
script protocol, so a model that learned one protocol's error shape
transfers directly. The format token is spliced into the system prompt
via `%q` (twice - header and rule 2); it has been validated against
the format constraint, so it carries no metacharacters that could break
the prompt.

The format prompt has no command surface: no `## ALLOWED COMMANDS`
(the model produces content, not a runnable script, and `-c` is
rejected by the parser in this mode). The user message is the same
shape as the script prompt's: ATTACHMENTS (files only, since `-c` is
rejected), `## REQUEST`, and the `## REQUEST DATA` block, with the
inference note using "output". The sanitization note stays, as the
model still sees the replacement runes. The two prompts are kept as
separate consts (`busyboxPrompt`, `formatPrompt`) rather than
parameterizing one, so the SCRIPT and OUTPUT contracts stay distinct
and a change to one cannot silently affect the other.

## Argument framing (execute vs dump vs format)

`BuildOptions.Output` distinguishes a script that will **execute**
from one that will only be **dumped**; format mode ignores it. The
three cases frame the args differently in the user message, but all
three now number the lines `N:LITERAL` with the `N` explained in a
`---`-delimited footer note:

- **Execute** (`Output == false`, script prompt): a `## REQUEST DATA
  IN "$@"` header, then one `N:<arg>` line per arg (1-indexed), then
  the `---` footer saying `N` is both the line number and the index
  in `"$@"` (and `$1`, `$2`, etc), plus the run-time notes (prefer
  literals for simple cases; quote-escape warnings; sanitization
  warnings). The indexing tells the model where each arg lands in
  `"$@"` at execution time.
- **Dump** (`Output == true`, script prompt): a `## REQUEST DATA`
  block via the shared `writeData` helper, one `N:<arg>` line per
  arg, with the inference note using "script". `"$@"` is never
  mentioned - the script never runs, so `"$@"` is meaningless.
- **Format** (`formatPrompt`): the same `writeData` block, with the
  inference note using "output".

`writeData(b, args, verb)` is shared by the dump and format paths, so
the two cannot drift apart. The execute path is separate because its
`"$@"` indexing and quote/sanitization notes are specific to a
script that runs. No data block is fenced anymore; the numbered
`grep -n`-style lines made fences redundant, and a fence would
visually collide with the `---` footer separator.

## Attachments (-c help and -i files)

`writeReferences` writes the user message's `## ATTACHMENTS`
section, which is where all reference material lives: one `###
OUTPUT: <name> --help` subsection per `-c` command that captured
help, then one `### FILE: <path>` subsection per `-i` file, in flag
order. `<path>` is exactly the path given on the command line, so
the script can read the file by the name it was given.

A file's lines are rendered `N:LITERAL` with `N` the real line
number and empty lines included as bare `N:`; the file's trailing
newline is a terminator, not an empty line (trimmed before the
split). Each FILE subsection ends with a `---` and the footer saying
the up-to-date file body is inlined above, numbered as in `grep -n`
- so the `N:` prefix needs no explanation the prompt does not
give elsewhere, and the model knows the snapshot may be stale and
should re-read the file (the path is right there in the header) when
freshness matters.

The section is emitted in all three modes (execute, dump, format):
`writeReferences` takes the commands and the files together, the
header is lazy (written only when a subsection actually follows),
and nothing is written when both lists are empty. The lazy header
carries the sanitization note ("Any \ufffd replaces non-printable
characters...") unconditionally - one note, not one per subsection.

File lines, `--help` lines, and the subsection header names are
sanitized with `sanitizeArg`, each multi-line blob one line at a
time so the layout survives. The section precedes the request, and
when it exists a blank line separates it from `## REQUEST`.

Dropped designs (do not resurrect without discussing): one
REFERENCES header per command (single section wins); the section
in the system message (it is request data, so it lives with the
request; the system message keeps only the stable rules and command
surface); subheaders written as running commands (`### grep -n .
<path>`, then `### grep -n '' <path>`) - the explicit OUTPUT:/FILE:
anchors plus the footer won because the header must not pretend to
be the exact command that produced the body (empty-line handling
never quite matched the real grep); empty lines skipped in file
bodies - the numbered body now includes them so line numbers are
honest; the section named REFERENCES - ATTACHMENTS says what the
model should treat it as.

## Section grammar

System message, in order:

- The rules text.
- `## ALLOWED COMMANDS` - the `-c` command names on one line, then
  the busybox applets on the next. One section, always present (the
  applets are always non-empty), because the applet list is the
  default command surface even with no `-c`.

User message in order:

- `## ATTACHMENTS` - only when at least one `-c` command has captured
  `--help` output or at least one `-i` file was given; the
  sanitization note, then one `### OUTPUT: <name> --help` subsection
  per command with help (in flag order, help sanitized line by
  line), then one `### FILE: <path>` subsection per file with its
  numbered body, `---`, and staleness footer (see
  "Attachments (-c help and -i files)"). Precedes the request, so
  the model reads the reference material before the task it serves.
  Commands without `--help` support are still allowed (they appear
  in ALLOWED COMMANDS) but get no subsection; they are not crippled,
  they simply have no reference.
- `## REQUEST` - the request text verbatim, multi-line supported
  (joined from stdin with newlines when the request came via `-`).
- `## REQUEST DATA IN "$@"` (execute) or `## REQUEST DATA`
  (dump/format) - only when arguments exist; one `N:<arg>` line per
  argument, then `---` and the contract notes (see "Argument
  framing" and sanitization below).

## Argument sanitization

Arguments may contain bytes the model should not see raw: newlines
would break the one-argument-per-line block format, and other
unprintables are noise at best. Sanitization replaces each newline,
carriage return, or unprintable rune with the Unicode replacement
character (U+FFFD, rendered as `<?>` in some fonts).

The crucial property - and why the prompt carries the two notes -
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
parser will realistically receive - treat the two as one system.
