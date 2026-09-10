# Account validation

Accounts have one explicit validation entry point:

```text
POST /admin/accounts/{id}/validate
Authorization: Bearer <admin key>
```

For an account with a configured source, the gateway selects the first source,
model, and declared protocol in configuration and runs the same bounded stream
validation used by **Validate source**. The response includes the account,
source, model, protocol, output/completion result, and whether the redacted
evidence was saved. A disabled source may still be checked explicitly; the
check never enables it or approves it for Auto routing.

For an account with a browser profile but no source, the endpoint performs the
provider's existing browser authentication check. Browser authentication is
reported separately from model generation and does not submit a prompt.

The action is authenticated and same-origin protected, does not retry an
ambiguous request, and never returns credential values, request bodies, URLs,
or upstream error text. Accounts displays the resulting health, authentication
state, capacity, and last validation alongside the existing browser evidence.
