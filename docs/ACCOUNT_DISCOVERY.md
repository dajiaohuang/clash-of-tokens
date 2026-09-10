# Account discovery

`POST /admin/discovery/accounts` returns bounded candidate metadata for the
account setup flow. It combines protected-vault references and configured
credential environment names. Callers may set `scan_browsers` to read standard
Chrome, Edge, Brave, Chromium, Vivaldi, Opera, Firefox, and Arc profile
metadata. The scan is read-only: it does not launch a browser, inspect cookie
databases, submit credentials, or run a provider request.

Each candidate contains its kind, origin, availability boolean, catalog provider
suggestions, and a next action such as `bind_credential` or
`create_browser_profile`. Values are intentionally absent. A provider match is
an exact catalog-domain or configured-source suggestion, not proof that the
account is authenticated. Binding and login remain explicit follow-up actions.

The response also reports five discovery channels. Browser and environment
channels are read-only gateway observations; existing profiles are configured
metadata. Password-manager exports and CLI sessions deliberately report
`manual_export_required`: the gateway does not scrape private manager stores or
CLI token files.
