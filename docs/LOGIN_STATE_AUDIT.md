# Login-state audit — 2026-09-15

## Current flow and scope

Select an installed browser's existing profile connection → review the full provider
catalog → confirm → visit compatible HTTPS destinations and capture obtainable sessions.
The scan is no longer restricted to configured accounts and never creates a blank
profile. Unsupported catalog entries remain visible with a reason, not a success.
This audit covers code paths; real user-session capture has not been accepted live.

## Login-state workbench redesign

The dedicated Login states page separates browser authorization, selectable catalog
scope, scan activity and saved account/session management. Website authentication
is shown as dated evidence, not current model availability. Account validation and
routing enablement are separate from capture. Validation UI warns about possible
model requests, charges and enablement; saving material no longer invokes validation.
The browser selector follows frontend-design guidance: a quiet browser rail,
a focused authorization/action area and a separate evidence inventory. Existing
account data is preserved without migration or deletion.

Latest-scan recovery and redaction have synthetic tests. Missing authorization,
browser switching and the management dialog were checked in the local UI. Real
browser-session capture remains user-authorized live acceptance, not a unit-test
claim.

## Findings and changes

1. Password database scanning/decryption, CSV/JSON password-manager parsing,
   secret-bearing import previews and import UI were removed. Legacy import endpoints
   reject requests. Existing encrypted account material and recovery backups remain.
2. Discovery reads only a bounded, validated DevToolsActivePort rendezvous file
   under standard installed browser roots. The endpoint is fixed to loopback.
   No password database, cookie database or encryption-key extraction is performed.
3. Preview issues a single-use five-minute ticket bound to the selected connection
   and configuration version. Start requires confirmation and unchanged connection
   metadata. Work is sequential, bounded to 20 minutes, cancellable, and not resumed
   after gateway restart. The connection has a 45-second native approval window.
4. One newly owned tab visits eligible catalog destinations (12-second site bound).
   Cookies are requested for the declared destination only, bounded to 256 cookies
   and 64 KiB. Partitioned cookies are omitted because generic headers lose their
   partition context. No browsing-history or blanket localStorage/token dump occurs.
5. Cookie candidates are saved in protected account storage and associated with a
   stable per-browser/provider account ID. Browser-only sessions retain an external
   profile reference when a supported check establishes authentication. Existing
   imported accounts are not overwritten. Captured accounts remain disabled and
   unverified until explicit invocation validation; cookies alone do not prove login.
6. External profiles cannot be launched or replaced by either account-login or
   draft-setup launch. Closing the scanner closes only its owned tab, not personal
   tabs or the user's browser. Cancel/version checks prevent later queued writes.
7. Configuration/evidence/results contain references and status, never cookie values.
   A failed configuration commit removes the new credential when possible. Old
   credential revisions are retained. Vault/config writes are not a cross-store
   atomic transaction; crash recovery can require unbound-reference review.

## Coverage ledger

| Area | Implementation | Evidence boundary |
| --- | --- | --- |
| Browser selection | browsermeta/connection.go, discover.go | Standard roots only; rendezvous presence is not a successful connection |
| Queue/coverage | api/browser_login_batch.go | Full catalog ledger; only implemented cookie/browser-capable adapters attempted |
| Tab ownership | browserexec/owned_cdp.go | Remote connection; new owned tab; no browser process launch |
| Capture/storage | browsermeta/cookies.go, credentials, saveScannedSession | URL-applicable nonpartitioned cookies; protected storage; candidate not authentication proof |
| Authentication | browserauth/page.go, check.go | ChatGPT, Claude, Blackbox read-only checks; no model prompts |
| Existing account checks | browser_login.go, account_material.go | Expected identity and stale verification binding checks retained |
| Invocation binding | session_acquire.go, account_validate.go | Explicit authenticated binding / model validation remain separate |
| Conversation state | sessions.go, session managers | Conversation clearing does not revoke website login |
| UI | control.js | Existing-browser choice, authorization guide, full-catalog review, confirmation, results, cancel |
| Password import | removed import implementation; rejecting legacy endpoints | No password-file ingestion; existing protected data preserved |

## Authorization and limits

Chrome 144+ supports user-enabled existing-browser access at
chrome://inspect/#remote-debugging followed by the browser's native permission prompt:
[official Chrome configuration guide](https://developer.chrome.com/docs/devtools/agents/get-started/configuration).
This is different from command-line debugging of a fresh isolated profile.
The user must enable access; the application never bypasses that decision.
Other Chromium families are attempted only if they expose compatible authorized
connection metadata. Their live compatibility is not certified. Firefox existing-
profile scanning is explicitly unsupported in this implementation.

Only the profile exposed by that authorized connection is scanned; all local user
profiles, custom roots and every storage mechanism are not enumerated. Native
browser-only session references depend on the browser remaining available and
may need rediscovery after restart. Cookie expiry and shared SSO scope still apply.
API-key-only, unimplemented, non-HTTPS and collector-unsupported entries are skipped.
MFA, CAPTCHA, new sign-ins and consent are not automated. Supplementary provider
fields may still be required. The latest scan metadata persists beside configuration
in a bounded .login-scan file; tickets and browser endpoints are excluded. Restart
marks unfinished scans interrupted, never resumes them. Protected account material,
configuration and authentication evidence also persist.

Local validation uses synthetic credentials and connection metadata. No real
provider login, cookie capture or model generation is claimed. On the inspected
host, Chrome and Edge did not yet expose authorized connection metadata.

## 中文结论

当前流程已改为：选择本机现有浏览器连接 → 查看完整服务商目录 → 二次确认 →
逐站尝试获取可取得的会话。不会再为批量扫描创建空白配置，也不限于已有四个账号。
可导出的 Cookie 候选加密保存并关联独立服务商账号；浏览器专用会话保存连接引用。
候选 Cookie 不等于已登录，网页登录也不等于模型调用可用，因此不会自动启用账号。

浏览器原生授权是前置条件，无法仅凭选择可执行文件接管现有登录态。
当前仅扫描授权连接暴露的配置与已实现采集方式；不承诺所有浏览器、所有配置、
所有站点或任意存储类型。没有实际获取用户会话作为验收。
账密导入功能已移除，已有加密资料和恢复备份未删除。
