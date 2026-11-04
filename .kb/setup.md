# Preface

This document describes the `now` configuration file
(`$HOME/.now.cnf`): the cnf layout of providers and models, the
resolution into a runtime `Options`, and the legacy `.now`
migration. The OpenRouter section covers provider routing in
particular, since that is where most of the routing subtlety lives.

The file is cnf, parsed via `cnfembed.ToJSON` and decoded with
`encoding/json` + `DisallowUnknownFields` - so any unknown key at
any depth is a load-time error. A leftover `$HOME/.now` in the old
flat `key=value` format is converted to `.now.cnf` and removed on
the first `Load`, comments carried over.

IMPORTANT: Read the .kb/agents.md file.


# Laws

## §1 - Configuration mistakes fail loudly, never silently

A cnf syntax error, an unknown key anywhere, a missing
`models.default`, a models entry without a provider or naming an
unknown one, a provider without an `api-url`, or an unsupported
`api-type` is a load-time error.


# Concepts

## Layout

The file holds a `providers` map of endpoints and a `models` map of
what a run uses:

    providers.local:
        api-url: "http://127.0.0.1:11434"
        api-key: "abc..."
        api-type: "completions-v1"

    models.default:
        provider: "local"
        provider-model: "llama3.1:8b"

- `providers.<name>.api-url` - Base URL of the OpenAI-compatible
  API, **required**. The client appends `/v1/chat/completions`
  (a trailing `/` is tolerated).
- `providers.<name>.api-key` - Bearer token for the
  `Authorization` header. Optional.
- `providers.<name>.api-type` - API dialect selector. Only
  `completions-v1` is supported; any other value rejected at load
  time. Defaults to `completions-v1`.
- `models.<name>.provider` - The providers entry that serves this
  model, **required**. Resolution fails loudly when missing or
  unknown.
- `models.<name>.provider-model` - The model name sent to the
  provider's API. Optional; defaults to the entry's name itself.

## Resolution

`setup.Load(model)` - `LoadFrom(path, model)` for an explicit path
- resolves `models.<name>`, or `models.default` when the name is
empty, through its provider into the flat `Options`
(`APIURL`/`APIKey`/`APIModel`/`APIType`) used by the completions
client. The `-m` option passes its key through; `-m` twice is an
ambiguous parse error.

## Legacy migration

When `.now.cnf` is absent but a flat `$HOME/.now` exists,
`internal/setup` converts it once: parse the old `key=value` file
(unknown key or malformed line aborts), render it as cnf with the
endpoint pairs inside `providers.local` and the comments carried
over (leading whitespace trimmed - cnf rejects an indented comment
before the first pair), then the old `api-model` value becomes
`models.default.provider-model`. The candidate content is parsed
back and compared field-by-field before anything is written; only
then temp file + rename (mode 0600) and the old file is removed.
Any failure leaves both files as they were. When `.now.cnf`
exists, `.now` is never touched.


## OpenRouter

OpenRouter (https://openrouter.ai) is an OpenAI-compatible aggregator:
one `api-url` (e.g. `https://openrouter.ai/api/v1`) fronts many
hosting providers for the same model slug. Routing - choosing *which*
backend serves a given request - is controlled by a `provider` object
in the request body. The `api-provider` key maps onto that object as
`{"order": ["<slug>"], "allow_fallbacks": false}`, pinning the request
to exactly one backend.

Provider slugs can be coarse or fine-grained: a base slug like
`google-vertex` matches all of that provider's endpoints (every
region and variant), while a suffixed slug like `google-vertex/us-east5`
or `deepinfra/turbo` targets one specific endpoint. The exact slug is
copyable from the model's page on OpenRouter.

### Options

The full `provider` object supports the following fields. Only
`order` and `allow_fallbacks` are used by `now` today; the rest are
listed for context.

- `order` - Provider slugs to try, in the given order. Disables
  default load balancing.
- `allow_fallbacks` - When `false`, the fallback tier is removed and
  only the preference tier is attempted: with `order` set, the list
  is walked in sequence and the request fails once it is exhausted;
  otherwise a single attempt is made on the top-ranked candidate.
  Defaults to `true`.
- `only` - Allowlist: the sole providers that may serve the request.
  Default load balancing still applies *within* the set.
- `ignore` - Blocklist: provider slugs to skip.
- `sort` - Rank providers by `"price"`, `"throughput"`, or
  `"latency"` instead of load balancing. As an object, also takes
  `by` and `partition` for multi-model fallback lists.
- `require_parameters` - Only route to providers supporting every
  parameter in the request. Defaults to `false`.
- `data_collection` - `"allow"` (default) or `"deny"` to restrict to
  providers that don't retain or train on data.
- `zdr` - Restrict to Zero Data Retention endpoints.
- `enforce_distillable_text` - Restrict to models whose authors allow
  text distillation.
- `quantizations` - Filter by quantization level (`int4`, `fp8`,
  `fp16`, ...).
- `preferred_min_throughput` - Soft preference: minimum tokens/sec
  (number or `{p50, p75, p90, p99}` percentiles). Non-matching
  providers are deprioritized, not excluded.
- `preferred_max_latency` - Soft preference: maximum latency in
  seconds, same percentile form.
- `max_price` - Hard cap, e.g. `{"prompt": 1, "completion": 2}`;
  also `request` and `image`. The request fails if no provider is
  cheap enough.

### Ordering

How the serving provider is chosen, in distilled form. Three
independent axes matter: the **candidate pool** (`only`/`ignore`),
the **ranking** of that pool, and the **retry behavior**
(`allow_fallbacks`).

1. **Default (no `order`, no `sort`): load balancing.** Providers
   that have been stable in the last 30 seconds are weighted by the
   inverse square of their price - a provider half the cost of
   another is four times as likely to be picked - and the remaining
   providers serve as fallbacks.
2. **`order` set:** your sequence *is* the ranking. Load balancing is
   disabled; providers are tried in the listed order, and providers
   outside the list remain eligible as last-resort fallbacks unless
   disallowed.
3. **`sort` set:** the pool is ranked by the chosen attribute
   (price, throughput, or latency) instead of load balanced, and
   tried in that order.

`allow_fallbacks: false` removes the fallback tier - the providers
that would be tried after the preference tier fails. What that means
depends on what the preference tier is:

| Preference     | Fallbacks on             | Fallbacks off     |
|----------------|--------------------------|-------------------|
| none           | top pick, then pool      | top pick only     |
| `order: [a,b]` | a, b, then pool          | a, b, then fail   |
| `only: [a,b]`  | top of {a,b}, then other | top of {a,b} only |

The pool and the fallback tier are different sets: `only` bounds
the pool, while the flag governs the second attempt. With `order`,
the second attempt lives outside your list; with `only` (or no
preferences), it lives inside the pool. The two settings are
therefore never redundant: `only: [a, b]` with the flag still drops
the in-pool backup, turning "best of a and b, then the other" into
"best of a and b, one shot". (The `only` row is inferred from the
flag's generic single-attempt semantics; the docs only exemplify the
`order` combination.)

The distinction between `order` and `only` is therefore ranking
semantics, not eligibility: `order: ["a", "b"]` means "always `a`
first, `b` as backup", while `only: ["a", "b"]` means "either of
them, router's choice, nobody else". With a single-element list the
two coincide, which is why `api-provider` maps unambiguously onto
`order: ["<slug>"]` with `allow_fallbacks: false`.
