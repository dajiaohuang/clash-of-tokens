# Protected credential storage

Account and source configuration contains `cred://` references. Credential
values live in a separate atomic vault file and are never included in the
configuration journal or credential-list responses.

| Platform | Protection | Runtime requirement |
| --- | --- | --- |
| Windows | Current-user DPAPI | The Windows user that created the vault |
| Linux | AES-256-GCM; random key in Secret Service's `login` collection | A session D-Bus and an available, unlockable Secret Service provider |
| macOS | AES-256-GCM; random key in native Keychain | A cgo-enabled build and an available, unlockable user Keychain |
| Other platforms | Unavailable | Credential writes fail closed |

The native backends use [99designs/keyring](https://github.com/99designs/keyring).
Only the selected native backend is allowed. File, shell-password-store and
kernel-session fallbacks are disabled. macOS uses the Security framework through
cgo; secret values are not passed as subprocess arguments. Keychain items are
not marked for iCloud synchronization. The OS may ask the user to unlock or
authorize access to its keychain.

On Linux and macOS, one random 256-bit key encrypts each vault. The file starts
with `COTK1`, a random 128-bit key identifier, then the random nonce and
authenticated ciphertext. The header is authenticated. Every write uses a new
random nonce. Keychain item identifiers are `vault-<hex identifier>`; macOS
items use the service `clash-of-tokens`.

Opening a nonexistent vault does not create a key. The first successful save
creates it. An existing vault never silently receives a replacement key. If
the original key is missing, access is denied, or ciphertext is damaged, the
operation fails and preserves the old file and in-memory records. A failed
filesystem commit after creating a key can leave an unused keychain entry.
Entries are not automatically deleted, because copied vault files may still
depend on them.

A vault-file copy alone cannot restore credentials on another machine: its
DPAPI identity or native keychain key is also required. Windows DPAPI vaults
and `COTK1` vaults are different formats; cross-platform migration is not
automatic. Gateway access-key storage is separate from this credential vault.

Direct writes of `username_password` material are accepted only as a bounded
JSON object containing non-empty username and password fields, matching the
password-manager import representation.

## Verification

The Windows suite covers envelope round trips, distinct nonces, every-byte
tampering, truncated ciphertext, missing keys, rejected keychain operations,
and preservation of an existing vault on failed updates. Native Windows DPAPI
has its own platform test.

Linux native create/reopen/update and unavailable-service behavior were
verified in Ubuntu 24.04 on WSL using synthetic credentials and an isolated
GNOME Keyring session. The runtime packages were extracted into test artifacts;
no system packages or existing user keyrings were modified.

Hosted Unix control-plane and race jobs use the explicit `cot_test_keyring`
build tag. That test-only implementation keeps random envelope keys in memory
for the duration of the process, so those jobs exercise vault and control-plane
behavior without weakening the production native-backend boundary. The
dedicated macOS job continues to exercise the real Keychain backend.

To repeat on Linux with `dbus-run-session`, `dbus-send` and
`gnome-keyring-daemon` available:

```sh
rtk go test -c -o .clash-tokens/credentials-linux.test ./internal/credentials
rtk proxy sh scripts/test_keyring_linux.sh .clash-tokens/credentials-linux.test
```

The script creates private XDG data/runtime directories, starts a separate
session bus and keyring daemon, runs synthetic tests, then removes its temporary
environment. `COT_KEYRING_DAEMON` may point at an extracted daemon executable.
It does not use the current user's keyring data directory.

Linux amd64 and macOS arm64 builds with cgo disabled compile. On macOS, that
build intentionally cannot write credentials. A cgo-enabled macOS build and
native macOS integration run have **not yet been verified** from the Windows
workspace. `TestNativeKeyringRoundTrip` is available when explicitly enabled
with `COT_TEST_NATIVE_KEYRING=1` in a suitable test environment; it removes only
the random key it creates. Do not equate cross-compilation with native runtime
verification.
