# Login-state audit — 2026-09-15

Scope: the local control-plane login/state flow, browser engines and process
ownership, account secrets, authentication evidence, session acquisition,
conversation inventory, management jobs, restart behavior, and front-end entry
points. This is a code-path audit, not a successful-login claim for every provider.

## Changes delivered

1. Removed password database scanning/decryption, CSV/JSON manager parsers,
   secret-bearing preview storage, import endpoints, import UI, and automatic
   import-to-provider matching. Removed the now-unused SQLite dependency.
   Existing account secrets and recovery backups were not deleted.
2. Added installed-browser discovery and a metadata-only site preview, followed
   by a single-use, five-minute confirmation. A backend queue processes up to
   64 configured website accounts, one at a time, with a 15-minute limit.
   Closing/reloading the panel does not stop the queue; explicit cancel or
   gateway shutdown stops remaining attempts. Queue progress is in memory;
   browser profiles and authentication evidence have separate persistence.
3. Removed the Chrome-only preparation requirement. Selected engines have
   account-specific profile directories and loopback debugging endpoints.
   Preparation never launches a browser. Another engine gets a separate
   directory; old profiles are preserved. Occupied unknown ports are not adopted.
4. Browser-only verification never dispatches source/model generation.
   Unsupported authentication checks remain unverified. The queue never reads
   stored login passwords, submits password forms, or approves SSO/MFA.
5. Account checks now enforce expected ChatGPT identity through the shared
   verifier. Runtime enablement rejects stale verification bindings after a
   browser runtime restart. Both login and invocation material are checked for
   missing/expired references when computing the displayed account state.

## Coverage and evidence boundary

| Area | Inspected implementation | Result / boundary |
| --- | --- | --- |
| Account material | `credentials/accounts.go`, `store.go`, platform protection | Account ownership + encrypted storage retained; old vault migration retains a recovery copy. |
| Imported secrets | removed `browserpassword`, password import handlers/parsers/preview | No scanning or password-file ingestion remains; legacy endpoints reject requests. |
| Launch | `browser_login.go`, `account_material.go`, `browser_processes.go`, platform launchers | Explicitly selected installed engine; persistent isolated profile; owned process handles; no personal-profile takeover. |
| Browser transport | `browserexec`, `browserbidi`, `browsermeta`, `account_fill.go` | Loopback CDP/BiDi; explicit per-provider scope. Manual account fill is a separate, user-triggered feature, not used by the batch queue. |
| Batch lifecycle | `browser_login_batch.go`, `jobs.go`, `control.go` | Confirmation, version checks, cancellation and bounded work; queue does not automatically resume after gateway restart. |
| Authentication | `browserauth/check.go`, `browser_login.go`, `account_material.go` | Automatic session endpoint probes currently cover ChatGPT, Claude and Blackbox. Other adapters return unsupported, not success. |
| Invocation-session binding | `session_acquire.go` | ChatGPT profile / Claude cookie binding only; expected identity/organization gates retained. This is not automatic generic API token acquisition. |
| Four account states | `account_material.go`, `verification.go` | verified / unverified / unfilled / invalid. Failed rechecks invalidate previously verified accounts. Runtime rejects stale stored approval. |
| Model validation | `account_validate.go`, source checks, evidence bindings | Explicit generation remains separate and can make upstream requests. The new browser queue does not invoke it. |
| Conversation state | `sessions.go`, `session/session.go`, adapter session managers | Conversation inventory and clear/expire are not browser login/logout. Clearing a conversation does not revoke website cookies. |
| Front end | `control.js` | Overview + Accounts entry, browser selector, site review, second confirmation, per-account result, stop button. No password-file or browser-password import path. |

## Remaining limits — not hidden successes

- Selecting a browser selects an executable, not its everyday personal profile.
  Existing personal-browser logins are not copied. A new isolated profile needs
  first sign-in; native persistent cookies/site storage can be reused afterward.
- Removing password import means the batch does not have a universal way to
  submit credentials. It opens the declared site and attempts session checks.
  MFA, CAPTCHA, consent and unrecognized login flows require interaction.
- Website sessions are native browser data. Cookies may receive browser/OS
  protection; local/session storage is **not** promised to be encrypted by the
  application's account-secret mechanism. Session-only cookies and site expiry
  policies may require re-login even when the profile directory survives.
- The four existing user accounts are not proof of supported automatic login:
  their providers require additional verifier/adapter work before the app can
  certify their website authentication. No real user account was logged in as
  part of this implementation validation.
- Account state is evidence-based and refreshed by explicit checks. There is
  no universal background expiry detector for every website. Manual/API-key
  configurations predating the four-state workflow retain their explicit policy.
- The batch is bounded to configured accounts with declared HTTPS destinations;
  it does not enumerate personal browsing history or create accounts for the
  entire catalog. An API host may not offer an interactive login page.
- Full per-engine, per-provider real-login and restart acceptance remains a live
  user-driven test. Unit fixtures are not substituted for that acceptance.

Chrome requires a non-default user-data directory for remote debugging from
version 136: [Chrome documentation](https://developer.chrome.com/blog/remote-debugging-port).
Firefox exposes its remote agent only when explicitly enabled:
[Firefox security documentation](https://firefox-source-docs.mozilla.org/remote/Security.html).

## 中文结论

已删除账密导入整条功能链，保留已有账号加密资料与恢复备份。新流程为：选择本机浏览器 → 查看站点 → 二次确认 → 后台逐站恢复／检查会话。各账号使用独立持久化浏览器配置，不接管个人浏览器。

必须明确：这不是“任何站点都能无交互自动登录”。首次登录、验证码、MFA、授权，以及尚无验证器的站点仍需要人工步骤；当前不会将用户的四个账号宣称为已登录。浏览器会话、API 调用凭证和对话历史是三种不同状态。队列取消或网关重启停止后续尝试，不删除浏览器配置，也不会自动注销网站。

历史审计和需求文档中关于密码 CSV/JSON 导入的条目已被本次变更取代。
