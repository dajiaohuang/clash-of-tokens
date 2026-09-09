# Meta AI

Native Go migration of the signed-in request path in
[g4f MetaAI.py](https://github.com/xtekky/gpt4free/blob/502b0e2e8d82292a39d2c3d9da07125322870583/g4f/Provider/needs_auth/MetaAI.py).
The source environment credential must contain your own session values:
`{"cookie":"YOUR_COOKIE_HEADER","lsd":"YOUR_LSD","fb_dtsg":"YOUR_FB_DTSG"}`.
No account or guest identity is created; the reference's synthetic birthday
and terms-acceptance mutation are not used. Login/refresh is not implemented.

One user text message with model `meta-ai` calls the private GraphQL mutation.
It returns the final `OVERALL_DONE` snippet, including revisions made during
generation. Streaming output is buffered. History, sessions, tools, media,
and optional follow-up source-list lookup are not implemented. Token usage is
not invented. Persisted GraphQL IDs may change; no real account verification
has been performed.

Local fixtures verify the signed-in form, mutation ID, fresh conversation and
threading IDs, terminal snapshot behavior, open-connection completion,
truncation, malformed text, media and application errors.
