# TinyCMS Web

`tinycms-web` uses the challenge and chat contract from
[OmniRoute at ba597b631d22d85e56db6982f24b7d1ebe238df9](https://github.com/diegosouzapw/OmniRoute/blob/ba597b631d22d85e56db6982f24b7d1ebe238df9/open-sse/executors/tinycms.ts).
It requests `/api/challenge`, executes the pinned WASM signer in a temporary
Chrome blank tab with its real canvas implementation, and posts signed headers
to `/api/openai/oneapi/v1/chat/completions`. The Go binary embeds the signer;
Node and a separate reverse proxy service are not required. The adapted
wasm-bindgen glue and binary come from `tinycmsSigner.ts`; its MIT notice is
preserved in `internal/providers/majorweb/tinycms-LICENSE.txt`.

Enable Chrome/CDP in the gateway browser configuration. Set the source's
`key_env` variable to JSON with your existing device identity and actual egress
IP, for example:

```json
{"key":"R<existing-device-identity>","client_ip":"<your-egress-IP>","user_id":"<optional-existing-user-id>"}
```

The gateway does not create identities or query a third-party IP discovery
service. The supplied IP must match the HTTP connection's egress address. It
rejects expired challenges and difficulty values above 16 to bound signing
work. Only one user text message is supported. Replies are buffered until both
an accepted finish reason and `[DONE]`; upstream errors, unsupported tools and
truncation fail. The `length` finish reason is preserved and usage is omitted.

`TestTinyCMSBrowserSigningContract` runs the actual embedded WASM in Chrome
against a local challenge/chat fixture. Enable it with `COT_TEST_CDP` set to
your CDP URL. The fixture passed on 2026-09-09. It verifies the request sequence,
signature header presence and identity/IP binding, not server acceptance of
the signature. Real TinyCMS access has not been tested; browser fingerprint
compatibility and current server challenge requirements remain unverified.
