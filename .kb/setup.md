# Preface

This document describes the `now` configuration file (`$HOME/.now`): the
supported keys, their defaults, and how they map onto the API request.
The OpenRouter section covers provider routing in particular, since
that is where most of the routing subtlety lives.

Configuration is a flat `key=value` file at `$HOME/.now`, loaded by
`internal/setup`. Blank lines and `#` comments are ignored; keys and
values are trimmed of surrounding whitespace and taken literally up
to end of line - there is no quoting or escaping.

IMPORTANT: Read the .kb/agents.md file.


# Laws

## §1 - Configuration mistakes fail loudly, never silently

An unknown key, a malformed line, a missing `api-url`, or an
unsupported `api-type` is a load-time error.


# Concepts

## Options

The complete set of configuration keys:

- `api-url` - Base URL of the OpenAI-compatible API, **required**.
  The client appends `/v1/chat/completions` (a trailing `/` on the
  value is tolerated).
- `api-key` - Bearer token sent in the `Authorization` header.
  Optional; omit for endpoints that don't need authentication.
- `api-model` - Model identifier sent in the request body.
  Defaults to `default`.
- `api-type` - API dialect selector. Only `completions-v1` (the
  OpenAI-compatible chat completions API) is supported; any other
  value is rejected at load time. Defaults to `completions-v1`.
- `api-provider` - OpenRouter provider slug pinning the request to a
  specific backend (see below). Empty (the default) means no
  preference and the endpoint's default routing applies. Ignored by
  non-OpenRouter backends.


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
