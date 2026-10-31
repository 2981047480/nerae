# 待批准设备列表 HTTP 接缝

## 范围

`NewApprovalListHandler` 构造未挂载的 `GET /me/login-approvals` handler：从 bearer 会话确定主体，调用真实待批准列表事务，在响应前重新检查会话及列表项时效，按 OpenAPI 验证输出并设置 `Cache-Control: no-store`。不接受调用方提供的主体、查询参数或请求正文；未知错误不返回底层信息。列表只是信息展示，不授予批准、兑换、设备信任或业务访问权限。

事务完成后的复验降低过期或撤销时返回旧信息的窗口；网络响应发出后申请仍可能随时变化，后续决定必须再次独立鉴权。超过 100 条、响应校验失败或条目在复验期间过期时整份返回不可用，不返回截断列表。

## 验证

- `go test -race -count=1 -run '^TestApprovalListHandler' ./internal/modules/auth/transport`（使用本机 Go 1.27.3 和离线模块缓存）：退出 0；覆盖空列表、错误/重复 bearer、非法方法/路径/正文、错误脱敏、响应合同、会话失效及响应前条目到期。
- `PYTHONDONTWRITEBYTECODE=1 python3 -B docs/evidence/A01/A01-23/mysql_redis_http_smoke.py`：退出 0。隔离 socket MySQL/Redis、真实会话及审批 store、真实 handler 与本地 TLS，在 Go race 下验证本人看到新设备申请及匹配码、另一合成主体与匿名请求无权读取、超 100 条失败关闭、过期申请不显示、主体停用后拒绝；无 token、审批 secret 或 nonce 泄漏。测试夹具只存在于本次私有数据库，脚本在成功后清除自己的目录。
- 合入集成提交 `89075a8` 后复跑上述两个命令，均退出 0；独立复核确认响应前过期检查及跨主体隔离边界，未发现新的 P0/P1。

## 限制

该 handler 没有挂载到生产进程；没有 UI 接入、真实邮件、小程序账号或真实设备。跨身份批准、请求安全兑换、幂等回执和恢复码尚未完成，不能据此宣称双设备登录可用。尚未实测网络过程中撤权与响应发出完全重叠；任何后续批准动作仍须在独立事务内检查当前授权。
