# HuggingChat

Native Go product protocol from the [pinned HuggingChat reference](https://github.com/xtekky/gpt4free/blob/502b0e2e8d82292a39d2c3d9da07125322870583/g4f/Provider/needs_auth/hf/HuggingChat.py).

Set `KeyEnv` to your own complete Hugging Face chat cookie string and configure an explicit product model ID from the site's `/chat/api/v2/models` list. This is the chat website adapter, not the official inference API. Login and browser TLS impersonation are not implemented, and no live account validation has been performed.

Each request creates a conversation, reads its last message ID, and submits a multipart `data` field. NDJSON `stream` tokens become OpenAI deltas; `finalAnswer` terminates and closes the connection immediately. EOF without that record, upstream errors and media output fail. One user text message is accepted; history, session continuation, tools, media and unsupported controls return 422. Created conversations remain in the upstream account; the reference provides no deletion contract used here.

Contract tests verify the three endpoint sequence, parent ID, multipart data, isolated cookie, streaming and buffered conversion, termination on a still-open connection, truncation and redacted in-band errors. Input, upstream and converted output are bounded.
