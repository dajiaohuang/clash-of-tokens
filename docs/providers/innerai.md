# Inner.ai

Native Go implementation of the [pinned Inner.ai product protocol](https://github.com/OmniRoute/OmniRoute/blob/ba597b631d22d85e56db6982f24b7d1ebe238df9/open-sse/executors/inner-ai.ts). `KeyEnv` accepts `{"token":"your-token-cookie","email":"your-email","device_id":"your-device"}`. Device ID and an email-shaped subject may also be read from JWT claims; they are request hints, never authorization decisions. Profile lookup, browser login and refresh are not implemented, so provide missing identity fields explicitly.

The adapter fetches `/api/v1/ai_models` from the fixed platform host on every request and requires an exact enabled `llm_model` match with an ID. It does not substitute another model or fabricate an ID when lookup fails. Chat uses a fresh `session_id`, `context_type:no_context`, `temporary:true`, and the returned `ai_model` ID. Model availability and plan eligibility are ultimately enforced by the upstream service.

One user text message is supported. SSE `text/item` becomes output and `end_stream` closes immediately. Credit/rate errors and truncated streams fail. Contract tests cover isolated credentials, real catalog lookup shape, unmatched model rejection, request fields, open-connection termination and errors. No live account test has been performed.
