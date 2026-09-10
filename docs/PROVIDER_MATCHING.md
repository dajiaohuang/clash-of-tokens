# Provider matching

Credential imports and account discovery use a host-exact catalog match. The
catalog includes a small allowlist of official API and product-console aliases
(for example `chat.openai.com` for OpenAI and `claude.ai` for Anthropic).
Matching normalizes case and a trailing dot, but never matches a suffix or a
lookalike domain. A match only suggests a provider and credential kind; it does
not authenticate the account, copy a cookie, or enable routing.
