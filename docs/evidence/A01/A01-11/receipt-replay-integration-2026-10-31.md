# A01-11 登录结果重放集成复测

集成代码提交 `f294817` 依次运行 `docs/evidence/A01/A01-04/mysql_smoke.py`、`A01-07/mysql_smoke.py`、`A01-08/mysql_smoke.py`、`A01-11/mysql_smoke.py` 和 `A01-21/mysql_smoke.py`，均退出 0。每个脚本使用新建的仅 Unix socket 私有 MySQL 和 Go race 检查，并在成功后清理自己的数据库目录；A01-21 按登记顺序应用全部 14 项迁移。

覆盖首次请求和相同请求重放：可信设备取得原会话令牌、新设备取得原待批准申请、错误验证码只产生一次计数；并检查撤权、锁等待、回执过期或篡改、事务回滚以及回滚后重发验证码。重放结果在源事务结束后重新读取已提交状态并复核授权、设备证明及有效期。[会话](../A01-07/session-receipt-replay.md) / [设备申请](../A01-08/approval-receipt-replay.md) / [错误码](receipt-rejection-replay.md)

这仍是内部事务证据，不表示真实登录可用。完整 HTTP 登录响应和限流尚未接入；新设备批准后的兑换还没有安全重放，QQ 邮箱、生产服务、实际 App 与双设备同步均未验证。此前 `b3c59dc` 的完整 Go race 与静态契约检查见[原集成记录](receipt-integration-2026-10-31.md)，不套用为本提交的新一次全量检查。
