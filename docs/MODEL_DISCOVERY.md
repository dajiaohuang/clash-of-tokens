# Model discovery

Native OpenAI, Anthropic, and Gemini sources can request their provider model
list from **Discover models** or `POST /admin/sources/{id}/discover`. Requests
are bounded to twenty pages, ten thousand unique model IDs, and a two MiB
response per page. Pagination cursors are validated for repetition and size.

Where the provider supplies it, the discovery result retains display name,
owner, creation timestamp, description, input/output token limits, and
declared generation methods. These values are provider metadata; the gateway
does not infer tools, vision, quality, protocol support, pricing, or remaining
quota from names or descriptions.

Discovered models are never inserted into routing automatically. Choosing
**Configure model** creates a disabled, unrated, not-Auto-approved model with
the reported ID as its declared and canonical name. The user must explicitly
choose protocols, capabilities, rating evidence, enablement, and Auto approval
before the model can be routed. Partial lists remain marked partial so a page
limit cannot be mistaken for a complete inventory.
