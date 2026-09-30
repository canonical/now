# The 'now' tool

AI for sensitive terminal environments in a classic way.

## Contents

- [Overview](#overview)
- [Usage](#usage)
- [Configuration](#configuration)
  - [Recommended models](#recommended-models)
- [Arguments and the standard input](#arguments-and-the-standard-input)
- [Busybox and external commands](#busybox-and-external-commands)
- [Security and safety](#security-and-safety)
- [Sandboxing and isolation](#sandboxing-and-isolation)
- [Plain output and formats](#plain-output-and-formats)
- [Examples](#examples)
  - [Classic greeting](#classic-greeting)
  - [Tracing, quietly, on errors](#tracing-quietly-on-errors)
  - [One question, three data sources](#one-question-three-data-sources)
  - [Explicit argument labels](#explicit-argument-labels)
  - [Implicit arguments](#implicit-arguments)
  - [Implicit desired outcome](#implicit-desired-outcome)
  - [Interactive scripts](#interactive-scripts)
  - [Python scripts on-the-fly](#python-scripts-on-the-fly)
  - [Custom external commands](#custom-external-commands)
  - [External JavaScript API](#external-javascript-api)
  - [Arbitrary content summary](#arbitrary-content-summary)
- [License](#license)

## Overview

The command is a single binary named **now**, written in Go with no dependencies outside of the standard library, that
accepts an arbitrary request in human language, prints a script for approval that performs the operation, and once
approved hands the script onto _busybox ash -e_ for execution.

```
$ now "sum the even numbers" 1 2 3 4 5
printf '1\n2\n3\n4\n5\n' | awk '$1 % 2 == 0 { s += $1 } END { print s }'
[ ENTER | CTRL-C ]
```

To perform its job the command sends a query to a v1 completions-compatible LLM API to generate a script to do the requested task.
The remote model only has access to the request and the provided data, and the generated script must solve the requested task
as a one-shot operation, without going back to the model again. The request may require reading local content, but if you do
not explicitly provide the content, the model must construct the script to access the required content and autonomously
solve the task locally during the script execution.

```
$ now "how many users are in this system"
wc -l /etc/passwd
[ ENTER | CTRL-C ]
```

Arguments and standard input are used as context for the script generation:
```
$ now "how many users are in this system" /chroot/passwd
wc -l < /chroot/passwd
[ ENTER | CTRL-C ]
```


## Usage

```
Usage:

  now [options] "<request>" [<arg> ...]

Options:
  <request>         Natural language request.
  <arg>             Data made available to the model and script, ordered.
  -                 Read either the request or the arguments from stdin.

Running control:
  -y                Auto-approve the generated script without asking.
  -q                Auto-approve and also hide the script before running it.
  -t                Trace each script command to stderr as it executes.
  -b                Buffer script output and only show it on failure.
  -c <cmd>,...      External command names from $PATH for the script to use.

Sandbox mode:
  -s                Enforce sandbox mode even without -r -w -n.
  -r <path> -r ...  Enforce sandbox mode and allow read-only access to path.
  -w <path> -w ...  Enforce sandbox mode and allow read-write access to path.
  -n                Enforce sandbox mode and allow network usage.

Output mode:
  -o <path>         Just write the content. Default -f from file extension.
  -f <format>       Just output content in the given format.

Boolean flags may be bundled together.
```


## Configuration

The configuration is loaded from `$HOME/.now` using a simple _key=value_ format:
```
api-url=http://127.0.0.1:11434
api-key=abc...
api-model=default
api-type=completions-v1
```
The `api-model` key selects the model name sent to the API. It defaults to `default` if unset.
The `api-type` key selects the API kind; only `completions-v1` is supported for now, and it is the default, so the key may be omitted.

If you run _now_ without a valid configuration, it will propose a script for creating it.


### Recommended models

All the testing and examples were done with _Qwen 3.8 27B NVFP4_ running locally, but any model that does well on terminal benchmarks will do well writing scripts.

## Arguments and the standard input

Information related to the request is made available to the model either via command line arguments, or via the standard
input by using the classic `-` argument. All of these are valid command lines:

```
$ echo "do something" | now - path
$ find /tmp/foo | now "do something" -
$ find /tmp/foo | now "do something" before - after
```

The provided information via either method is flattened into a single ordered sequence which is both communicated to the model
and then made available to the generated script under `"$@"`.

This example demonstrates the kind of flexibility and clarity that can be achieved as a side effect of this choice:

```
$ echo file1 file2 | now "cp FOO files to BAR dirs" FOO: - BAR: /one /two
for f in file1 file2; do
  for d in /one /two; do
    cp "$f" "$d/"
  done
done
[ ENTER | CTRL-C ]
```

The key to the success when using _now_ is realizing that arguments have no explicit meaning. It's up to you and the model to
define it, and most often the model can tell what you mean with no further help. 


## Busybox and external commands

The _busybox_ project was chosen as the execution environment because it's a battle tested, compact, fast, and rich environment
where most important commands are supported without even executing an external process. Being mostly standalone also
facilitates the sandboxing features described in the respective section.

With that said, _now_ can actually work with any external command that is in the `$PATH` and supports the
ubiquitous `--help` convention.

Here is a simple example:

```
$ cat download.sh
#!/bin/sh
[ "$1" = "--help" ] && echo "Usage: download.sh --output=<file> --source=<url>"

$ now "fetch the file" http://example.com/foo.txt bar.txt
wget -O "$2" "$1"
[ ENTER | CTRL-C ]

$ now -c download.sh "fetch the file" http://sample.com/foo.txt bar.txt
download.sh --source="$1" --output="$2"
[ ENTER | CTRL-C ]
```

As expected given the state of modern models, the hints available are enough for it to imply the task and 
assign the arguments properly.

Commands supporting more complex APIs may choose to differentiate their output by checking if `$HELP_FOR_AGENT`
is set to `1` when processing the `--help` argument. See [External JavaScript API](#external-javascript-api) in the examples.


## Security and safety

The only data that is ever sent to the model is the data you explicitly provided via the arguments and standard input. There's
no tool calling, no multi-turn decision making that the model can do by itself, no environment variables. If you don't put it
in, the model does not see it.

Here is a simple example that demonstrates this:
```
$ touch foo/1.txt foo/2.txt bar/three.txt bar/four.txt

$ now "use the name style of foo for bar files"  foo/ bar/*
for f in bar/*.txt; do mv "$f" "${f%.txt}"; done
[ ENTER | CTRL-C ]

$ now "use the name style of foo for bar files"  foo/* bar/*
mv bar/four.txt bar/4.txt
mv bar/three.txt bar/3.txt
[ ENTER | CTRL-C ]
```

The only difference between these two commands is the `foo/` vs `foo/*` command line argument, but it made a big difference
because with the filenames from `foo/*`, the model can trivially understand that the task is to change the number format in
the names, but without the data and the explicit prompting, the best it could do is to use the name of `foo` itself
and drop off the `.txt` extension.

Most importantly, since the context is security and safety, in the first case the model _did not read the directory by itself._
You did not give it the data, and it cannot have it. This is part of the core principles of _now_: the tool is small, has no
dependencies outside of the Go standard library itself, and the model it uses cannot read anything you do not hand to it. The
impactful data operations are done completely locally, explicitly, in a declared way. These principles are why the tool exists,
and why I feel confident in using it even in sensitive environments.


## Sandboxing and isolation

The section above covers security and safety from an architectural standpoint: the trust model, the separation of concerns,
the review and approval process. Even then, there are times when this might not be enough; for example, when the complexity
of the requested task and the script is too large and boring to review in detail, or because _now_ is being used unattended.
For these cases, _now_ supports stronger sandboxing and isolation so that the generated scripts cannot get outside of the
boundaries defined. This support is available as long as Bubblewrap's _bwrap_ is available on the `$PATH` and the running
system's constraints do not get in the way.

Here is an overview of the relevant parameters:
```
  -s              Enforce sandbox mode even without -r -w -n.
  -r path -r ...  Enforce sandbox mode and allow read-only access to path.
  -w path -w ...  Enforce sandbox mode and allow read-write access to path.
  -n              Enforce sandbox mode and allow network usage.
```

To explore the problem and the solution to this issue, consider this example:
```
$ now -s "write the epoch" timestamp.txt
date +%s > timestamp.txt
[ ENTER | CTRL-C ]

$ now -s "how many users are in this system"
wc -l < /etc/passwd
[ ENTER | CTRL-C ]
```
For the first command we guided the model to our choice of path, but for the second one
the model used its internal knowledge to attempt to solve the task at hand. Both are valid,
useful, and work correctly. The problem, as stated earlier, is when you intend to run
these commands without supervision.

With sandboxing these cases become safe again (_-q_ for approve quietly):
```
$ now -q -s "how many users exist in this system"
sh: can't open /etc/passwd: no such file
error: script failed: exit status 1

$ now -q -r /etc/passwd "how many users exist in this system"
19

$ touch date1.txt date2.txt
$ now -w date1.txt "write the epoch" date1.txt date2.txt
e=$(date +%s)
echo "$e" > date1.txt
echo "$e" > date2.txt
[ ENTER | CTRL-C ]

sh: can't create date2.txt: Read-only file system
error: script failed: exit status 1
```


## Plain output and formats

The core purpose and behavior of _now_ is centered around the execution
of tasks with complete control of data shared and supervision of the outcome
so it can universally assist in everyday terminal work, even inside sensitive
environments where "smarter" tools are not welcome.

As a bonus feature, _now_ also supports writing out the generated scripts
as well as arbitrary code and data in any language and format supported
by the underlying model. Obviously, if you generate code this way, make sure
to read and understand it before running.

To output shell scripts, provide the `-f sh` flag. This includes further
instructions such as external command reference, but still follows a different
path from traditional execution because the script will need to use the request
data as literals and not as provided parameters.
```
$ now -f sh "print A to B" A=1 B=5
seq 1 5
```

Any other format will use a more general approach so arbitrary code and
data may be produced. The `-f` flag value must be a clean string formed by the
characters `[-.a-z0-9]`, but is otherwise unconstrained. For example:
```
$ now -f py "print A to B, compact" A=1 B=5
print(*range(1, 6))

$ now -f txt "print A to B, compact" A=1 B=5
1 2 3 4 5

$ now -f json "print A to B, compact" A=1 B=5
[1,2,3,4,5]
```

Both arguments and standard input work as usual:
```
$ echo A=1 | now -f txt "print A to B, compact" B=5 -
1 2 3 4 5
```

Any of these can also be directly written onto a file:
```
$ now -o out.sh "print A to B" A=1 B=5
$ cat out.sh
seq 1 5
```
Without the `-f` flag, the file extension becomes the default format,
and with no extension it falls back to shell.


## Examples

All of these examples were generated on a local _Qwen 3.8 27B Q4_ model running on _llama.cpp_.

### Classic greeting

```
$ now "greet the world"                                          
echo "Hello world!"
[ ENTER | CTRL-C ]
```

### Tracing, quietly, on errors

```
$ ./now -tqb "print the cwd"

$ ./now -tqb "print the cwd then fail"
+ pwd
/home/ubuntu
+ exit 1
error: script failed: exit status 1
```

### One question, three data sources

Path from internal model knowledge:
```
$ now "how many users have bash as a shell?"
awk -F: '$7=="/bin/bash"' /etc/passwd | wc -l
[ ENTER | CTRL-C ]
```

Path from stdin:
```
$ cat /etc/passwd | now "how many users have bash as a shell?" -
printf '%s\n' "$@" | grep -c ':/bin/bash$'
[ ENTER | CTRL-C ]
```

Path from argument:
```
$ now "how many users have bash as a shell?" /chroot/passwd
awk -F: '$7 ~ /bash/' /chroot/passwd | wc -l
[ ENTER | CTRL-C ]
```

### Explicit argument labels

```
$ echo file1 file2 | now "cp FOO files to BAR dirs" FOO: - BAR: /one /two
for f in file1 file2; do
  for d in /one /two; do
    cp "$f" "$d/"
  done
done
[ ENTER | CTRL-C ]
```

### Implicit arguments

```
$ now "download the file" https://example.com/index.html test.html
wget -O test.html https://example.com/index.html
[ ENTER | CTRL-C ]
```

### Implicit desired outcome

```
$ touch foo/1.txt foo/2.txt bar/three.txt bar/four.txt
$ now "use the name style of foo for bar files"  foo/* bar/*
mv bar/four.txt bar/4.txt
mv bar/three.txt bar/3.txt
[ ENTER | CTRL-C ]
```

### Interactive scripts

Scripts have access to stdin as well, as long as `-` isn't used as an argument to input request data.

```
$ now -c python3 "in a loop, ask for a line, run it on python, repeat"
while true; do
  printf '>>> '
  read line || break
  [ -z "$line" ] && break
  python3 -c "$line"
done
[ ENTER | CTRL-C ]

>>> print("hi")
hi
```

### Python scripts on-the-fly

```
$ echo 9 | python3 -c "$(now 'print a py program that prints the sqrt of the number from stdin')"
cat << 'EOF'
import math
print(math.sqrt(float(input())))
EOF
[ ENTER | CTRL-C ]

3.0
```

Quietly using the plain mode, for the brave:

```
$ echo 9 | python3 -c "$(now -f py 'print the sqrt of the number in stdin')"
3.0
```

### Custom external commands

```
$ cat download.sh
#!/bin/sh
[ "$1" = "--help" ] && echo "Usage: download.sh --output=<file> --source=<url>"

$ now -c download.sh "fetch the file" http://sample.com/foo.txt bar.txt
download.sh --source="$1" --output="$2"
[ ENTER | CTRL-C ]
```

### External JavaScript API

The app is in development so the model has no way of knowing it yet, but the tool
is documenting its own API via `--help` and `$HELP_FOR_AGENT`.

```
$ now -c any-store-cli2 "how many readers live in Berlin" library.db
any-store-cli2 library.db -e 'db.readers.find({"city":"Berlin"}).count()'
[ ENTER | CTRL-C ]

3
```

### Arbitrary content summary

```
$ now --help 2>&1 | now -f txt "why would I use this, in few words?" -
You use `now` when you want to turn a natural-language sentence into a
small, reviewable shell script that runs in a sandbox — one-shot, no
multi-turn LLM, no hidden tool calls. You see the script before it
executes, and busybox + sandbox flags keep it from touching anything
you haven't explicitly allowed.

In short: NL → script → approve → safe run, in one step.
```

## License

The _now_ project is made available under the terms of the Apache 2.0 license.
See the `LICENSE` file for details.