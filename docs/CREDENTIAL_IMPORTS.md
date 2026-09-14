# Account materials and removed password imports

Password import was removed on 2026-09-15 at the user's request.

- No browser password database discovery/decryption.
- No password-manager CSV/JSON parsing, selection, tickets, batch import or automatic provider matching.
- Old password-import and auto-match HTTP endpoints return 410.
- Existing encrypted account material and legacy recovery backups remain untouched.
- Manually entered account keys/passwords and provider-specific session binding are separate features; they were not removed.

Use **Overview → Choose browser → Review sites → Confirm and start**.
Only configured website accounts are in scope. Browsers keep persistent, isolated
account profiles; no personal-profile passwords or cookies are copied.
First sign-in and challenges may require the user. A successful browser check
does not prove API or model generation support.

See [the login-state audit](LOGIN_STATE_AUDIT.md).

## 中文

已按用户要求移除浏览器密码扫描、密码库解密、CSV/JSON 解析、预览选择、整批导入和导入后自动解析服务商。旧接口返回 410。没有删除已保存的账号加密资料或旧库恢复备份。

新入口为：首页 → 选择浏览器 → 查看并确认站点 → 确认并开始。仅处理已配置的网站账号；每个账号使用独立持久化配置，不复制个人浏览器密码或 Cookie。首次登录和安全验证仍可能需要用户操作。手工填写账号资料与站点会话绑定是独立功能，不属于此次删除范围。
