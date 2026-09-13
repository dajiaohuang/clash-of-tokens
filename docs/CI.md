# Continuous integration

The repository workflow at `.github/workflows/ci.yml` is the hosted
cross-platform verification boundary. Every push to `main`, pull request and
manual dispatch runs:

- the full Go test suite and `go vet` on Ubuntu, Windows and macOS. Unix
  control-plane tests use the explicit `cot_test_keyring` build tag, which
  keeps random test-only vault keys in the process so hosted runners do not
  depend on a desktop Secret Service or login Keychain;
- a dependency-free audit that rejects direct production logger sinks outside
  the gateway's fixed-category redaction boundary;
- the full Linux race detector suite;
- a cgo-enabled macOS test against the native Keychain backend using only a
  synthetic temporary vault entry; and
- native gateway builds for Linux amd64 and Windows amd64 with `CGO_ENABLED=0`,
and macOS arm64 with `CGO_ENABLED=1` so the shipped binary includes Keychain;
- create/restart/read/rotate/delete/canary checks against the exact binary
uploaded by each build job, using isolated native storage;
- real Chromium/Firefox provider fixtures and UI regression against a real Go
Admin API, with synthetic upstreams and test-only storage on hosted Linux;
- pinned `govulncheck@v1.1.4`, artifact SHA-256, embedded build metadata and
license notices for dependencies actually present in the binary.

These are workflow definitions, not a claim that this working tree has passed
hosted CI. Local evidence and remaining release gates are tracked in
[the next-release status](NEXT_RELEASE_STATUS.md).

The native Keychain job does not import a user account, read an existing
credential, or contact an upstream provider. It is deliberately separate from
the cross-compiled builds: a `darwin` binary built with cgo disabled is only a
compile check and cannot prove Keychain availability. Hosted CI also does not
prove live provider authentication, browser compatibility, physical-device
operation, signed release provenance, or distribution publication.
