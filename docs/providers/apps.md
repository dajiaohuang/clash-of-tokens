# Merlin, Sider, Monica and Raycast

These adapters call the products' own private endpoints from Go. They accept
one user text message and reject unsupported roles, history, attachments, tools
and session headers. All return buffered Chat Completions or buffered SSE;
none have been tested with a real product account. Local fixture success is
not evidence of current upstream access or subscription coverage.

| Adapter | Credential JSON in `key_env` | Model selection |
| --- | --- | --- |
| `merlin` | `{"token":"<your existing bearer>"}` | Exact Merlin model name |
| `sider` | `{"token":"<your existing bearer>"}` | Exact Sider model name; mismatched response model fails |
| `monica` | `{"cookie":"<your complete cookie>"}` | Pinned model-to-bot mapping in `monica.go`; unknown names fail |
| `raycast` | `{"token":"<your bearer>","device_id":"<your device>","signature_secret":"<your signing secret>"}` | Exact ID from the account's `/api/v1/ai/models` registry |

Merlin sends `/v1/thread/unified` with an empty context and a fresh chat ID.
It uses the existing bearer directly, without the reference's external token
generator. Its reference parser defines transport EOF as completion. The
adapter buffers until clean EOF or `[DONE]`, rejects transport errors and
malformed JSON, and preserves UTF-8 text without the reference's Latin-1
conversion. An application-level truncation followed by clean HTTP EOF cannot
be detected with this documented contract.

Sider sends `/api/chat/v1/completions` using a fresh empty conversation ID,
text-only `multi_content`, and an empty automatic-tool list. It validates
response codes and model identity and requires `[DONE]`. Text and reasoning
are returned separately. Tool events fail because tool delivery is unsupported.

Monica sends `/api/custom_bot/chat` with a new conversation and an explicit
welcome-to-question item chain. Supported model names map to upstream bot IDs.
The adapter accepts `finished: true` or `[DONE]`, rejects error codes and
truncated streams, and omits intermediate agent status. It does not create,
publish or edit custom bots, upload files, or enable memory.

Raycast fetches the account model registry, resolves the requested ID without
a default fallback, then sends `/api/v1/ai/chat_completions`. Its V2 signature
binds the timestamp, device ID and body SHA-256 through the documented ASCII
rotation and HMAC-SHA256. The signing secret must be supplied by the operator;
the reference's shared default is not included. Explicit finish reasons are
validated and `length` is preserved. Usage is omitted for all four adapters.

Tests cover endpoint and payload contracts, account versus caller credentials,
model mismatch/default rejection, invalid streams, and a Raycast signature
golden vector independently generated with Python's standard library. These
Go implementations were written for this gateway from protocol facts; the
reference services are not dependencies or required sidecars.

Pinned protocol evidence:

- [Merlin app.py, 24e6f382](https://github.com/cchking/merlin2api/blob/24e6f382bfb02c25dabc9056f564fa3936dbe1f0/app.py)
- [Sider client, 7801bb98](https://github.com/yeuxuan/sider2api/blob/7801bb982aaf28a3efdb69c119e2e6b40749ef03/src/utils/sider-client.ts), with `request-converter.ts` and `types/sider.ts` at that commit
- [Monica request types, d5617079](https://github.com/SimonUTD/monica2api/blob/d56170796e8081de4ee47b49f09e058769779f8b/internal/types/monica.go), with `internal/monica/sse.go` and `internal/utils/req_client.go` at that commit
- [Raycast protocol utilities, 8e9442ca](https://github.com/xxxbrian/raycast2api/blob/8e9442ca5d3c8cf6438a8582d0cdadfc5de173db/src/utils.ts), with `handlers/chat.ts` and `config.ts` at that commit
