# Token and declared-cost accounting

The gateway records token counts only when an upstream response declares them.
It accepts the common OpenAI/Anthropic `prompt_tokens`, `completion_tokens`,
`input_tokens` and `output_tokens` fields, their camelCase forms, and Gemini's
`usageMetadata` names. Anthropic input and output declarations may arrive in
different stream events; the observer combines them. A missing declaration is
kept unknown and is never estimated from text length.

Streaming responses are observed as they pass through. For non-streaming JSON,
the gateway captures at most 1 MiB while forwarding the body and parses usage
only when the bounded capture completes. Larger or non-JSON responses still
complete normally but report unknown usage. This keeps accounting from adding
an unbounded response buffer.

`GET /admin/status` source rows expose observed input, output and total tokens.
They also expose `cost_known` and `estimated_cost_usd`. The estimate multiplies
declared input/output counts by the operator-supplied model rates. It is shown
as known only when every recorded execution for that source in the current
runtime had both usage and both declared rates; otherwise the estimate is
withheld. It does not include cache, tool, image or subscription charges and is
not a provider invoice or a quota balance.

Counters are runtime observations and reset on process restart. Retries count
as separate execution attempts. A canceled or failed attempt may have consumed
provider usage; cancellation does not imply zero tokens. Validation requests
are included in the source counters and remain explicitly labeled in execution
events.
