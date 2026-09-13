# Credential import formats

Open **Credentials → Import export**, choose the format and a local export file,
preview the candidates, then select the entries to save. Imports create new
protected references or explicitly keep/replace an exact matching login. Bind
password references to **Login material** from Accounts. Imported passwords
are login material, not API keys or proof of authentication.
The protected metadata retains only the normalized source domain for later
provider matching; usernames and passwords remain encrypted and are never
included in previews or discovery responses.
See [platform storage requirements](CREDENTIAL_STORAGE.md) for protected-vault
backends and recovery limits.

| Export | Format selector | Login fields |
| --- | --- | --- |
| Generic / Google Password Manager | `auto` / `google-passwords-csv` | name, url, username, password |
| Bitwarden CSV | `bitwarden-csv` | name, login_uri, login_username, login_password |
| Bitwarden unencrypted JSON | `bitwarden-json` | type 1 items: name and login username/password/uris |
| 1Password CSV | `1password-csv` | Title, Website, Username, Password |
| KeePassXC CSV | `keepassxc-csv` | Title, URL, Username, Password |
| Proton Pass CSV | `protonpass-csv` | name, url, username, email, password |
| Dashlane credentials CSV | `dashlane-csv` | name or title, url, username, password |
| NordPass CSV | `nordpass-csv` | name, url, username, password |
| Apple Passwords CSV | `apple-passwords-csv` | Title, URL or Url, Username, Password |
| Chrome / Edge / Brave / Firefox / Opera / Vivaldi / Chromium / Arc | corresponding `<browser>-csv` | URL, username, password; case-insensitive header aliases |
| Safari | `safari-csv` | Title, URL, Username, Password |
| KeePass | `keepass-csv` | Title, URL or Web Site, User Name, Password |
| LastPass | `lastpass-csv` | name, url, username, password |
| Enpass | `enpass-csv` | Title, Website/URL, Username/Login Name, Password |

The matrix describes supported header schemas, verified with synthetic fixtures.
It does not certify every application/export version. Unsupported encrypted
formats and 1Password `.1pux` are not implied by the CSV selectors.

Previews hold at most four tickets for five minutes. Applying consumes the
ticket; canceling drops it. Only selected records are persisted. Replacement
requires the exact stored URL, username, credential kind and version; matching
email addresses alone never merge accounts across products.

Files are bounded to 4 MiB, 10,000 scanned manager rows and 1,000 login
candidates. The preview reports excluded non-web and non-password records.
Only HTTP(S) website entries with passwords are eligible. Plain domain strings
are normalized to HTTPS for matching; no connection is made during import.
Imported login material is stored as a protected `username_password` reference
whose value is one bounded JSON object with non-empty username and password
fields; it is not treated as an API key or proof of authentication.
Bitwarden multiple URIs become separate selectable candidates. Proton Pass uses
the default-match `url` column, retains email inside encrypted credential data,
and uses it as the username only when username is empty. It does not convert
Never-match URL rules into suggested bindings.

Encrypted manager exports, ZIP archives, passkeys, notes, TOTP, cards, custom
fields and password history are not imported. For Dashlane ZIP exports, select
the extracted credentials CSV. The application never scans or decrypts a
password-manager vault. It never deletes the user's export file.

**Credentials → Import token** supports a configured source's environment
variable or an explicitly named safe environment variable before a source
exists, plus selected Codex, Gemini CLI or generic OAuth JSON. Only the current
access token is imported. Refresh tokens and CLI account IDs are not copied;
that import path does not configure automatic refresh. The separate **Add OAuth
lifecycle** action accepts an explicit grant and refresh endpoint/client ID.
Access/refresh tokens remain encrypted; expiry, scope and account metadata are
visible. Concurrent refreshes coalesce, version checks reject stale writes, and
failures enter a one-minute backoff before retry or reauthorization.

**Credentials → Import browser cookies** reads cookies applicable to one
selected catalog provider URL from an enabled configured CDP or Firefox BiDi profile.
Only providers declaring cookie credentials are selectable. Preview exposes
the domain and count; saving re-reads current cookies into a new protected
reference. It opens and closes a blank tab without navigating to the provider.
Limits are 256 cookies, 64 KiB and ten seconds. Cookie presence does not prove
authentication. Bind the reference separately; adapters that use browser
profiles may require the original profile instead of imported cookie values.
Discovered OS profiles are not automatically opened or decrypted.

**Add external manager reference** uses official `op read op://vault/item/field`
or `bw get password|username <item-UUID> --raw`. Only the selected field is read;
no vault listing or export occurs. Saving a reference performs no lookup.
An explicit availability check or model request resolves it with a 20-second
deadline and 1 MiB output bound. Child environment variables are allowlisted,
and manager stderr is not returned. Real CLI authorization remains dependent on
the user's installed, authorized manager. These references currently represent
invocation values, not paired username/password autofill.

## Format references

- [Bitwarden export-compatible formats](https://bitwarden.com/help/condition-bitwarden-import/)
- [1Password CSV fields](https://support.1password.com/export/)
- [KeePassXC CSV exporter](https://github.com/keepassxreboot/keepassxc/blob/develop/src/format/CsvExporter.cpp)
- [Proton Pass CSV exporter](https://github.com/ProtonMail/WebClients/blob/main/packages/pass/lib/export/csv.ts)
- [Dashlane CSV template](https://support.dashlane.com/hc/en-us/articles/12843960410898-Import-my-data-using-the-Dashlane-CSV-template)
- [NordPass CSV fields](https://support.nordpass.com/hc/en-us/articles/360002377217-How-to-organize-CSV-file-for-import-to-NordPass)
- [Apple Passwords export](https://support.apple.com/guide/icloud-windows/export-passwords-icw3cb8e8853/icloud) and [field mapping reference](https://github.com/bitwarden/clients/blob/main/libs/importer/src/importers/safari-csv-importer.ts)
- [Google password CSV fields](https://support.google.com/accounts/answer/10500247?hl=en)
