# Credential import formats

Open **Credentials → Import export**, choose the format and a local export file,
preview the candidates, then select the entries to save. Imports create new
protected references. Bind those references from Accounts. Imported passwords
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
variable and selected Codex, Gemini CLI or generic OAuth JSON. Only the current
access token is imported. Refresh tokens and CLI account IDs are not copied;
automatic refresh remains unimplemented. The UI reports this before saving.

**Credentials → Import browser cookies** reads cookies applicable to one
selected catalog provider URL from an enabled configured CDP browser profile.
Only providers declaring cookie credentials are selectable. Preview exposes
the domain and count; saving re-reads current cookies into a new protected
reference. It opens and closes a blank tab without navigating to the provider.
Limits are 256 cookies, 64 KiB and ten seconds. Cookie presence does not prove
authentication. Bind the reference separately; adapters that use browser
profiles may require the original profile instead of imported cookie values.
Discovered OS profiles are not automatically opened or decrypted.

## Format references

- [Bitwarden export-compatible formats](https://bitwarden.com/help/condition-bitwarden-import/)
- [1Password CSV fields](https://support.1password.com/export/)
- [KeePassXC CSV exporter](https://github.com/keepassxreboot/keepassxc/blob/develop/src/format/CsvExporter.cpp)
- [Proton Pass CSV exporter](https://github.com/ProtonMail/WebClients/blob/main/packages/pass/lib/export/csv.ts)
- [Dashlane CSV template](https://support.dashlane.com/hc/en-us/articles/12843960410898-Import-my-data-using-the-Dashlane-CSV-template)
- [NordPass CSV fields](https://support.nordpass.com/hc/en-us/articles/360002377217-How-to-organize-CSV-file-for-import-to-NordPass)
- [Apple Passwords export](https://support.apple.com/guide/icloud-windows/export-passwords-icw3cb8e8853/icloud) and [field mapping reference](https://github.com/bitwarden/clients/blob/main/libs/importer/src/importers/safari-csv-importer.ts)
- [Google password CSV fields](https://support.google.com/accounts/answer/10500247?hl=en)
