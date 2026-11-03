# 新设备审批决定：事务与本机接口

本次从集成基线 `71aeeb1` 出发，补齐已有设备对新设备登录的批准/拒绝决定。决定和加密幂等回执在同一数据库事务中提交；相同请求重试时，必须重新验证当前账号、身份、会话、申请状态和到期时间。错误比较码、过期或无权访问的申请返回统一拒绝；数据库结果不确定时不宣称批准或失败。

HTTP 入口要求可信代理来源，先扣 IP/全局额度，再从已有会话解析主体并扣主体/申请额度，最后执行和复核事务。审批限流策略与邮箱登录策略分开配置：`rate_approval_window_seconds`、`rate_approval_subject_limit`、`rate_approval_attempt_limit`、`rate_approval_ip_limit`、`rate_approval_global_limit` 均为受保护配置的必填字段。服务启动时拒绝单申请超过 10 次或限流窗口短于申请有效期的配置；具体生产值仍须部署时核定。Redis 重启/数据恢复导致计数器丢失的部署风险尚未验证。

审批决定与训练请求复用现有 transport 幂等密钥 epoch，但各自使用独立的固定 SQL 策略。输入合同允许 4–8 位数字；当前签发和存储的比较码仍固定六位，其他长度只会作为不匹配拒绝。

## 已运行的验证

- `python3 -B docs/evidence/A01/A01-08/mysql_smoke.py`：退出 0，私有 socket MySQL 和 Go race 验证决定与回执的原子提交/回滚、同键并发重放、不同会话竞争、锁等待跨过申请过期、身份撤销先提交等场景。
- `python3 -B docs/evidence/A01/A01-14/redis_smoke.py`：退出 0，私有 socket Redis 和 Go race 验证审批独立限流、主体隔离、并发上限及 Redis 不可用时失败关闭。
- `go test -race -count=1 -run 'TestApprovalDecisionHandler' -v ./internal/modules/auth/transport`：退出 0，合成端口验证请求形状、授权和限流顺序、回执复核、状态映射及响应脱敏。
- `NERAE_RUN_API_ASSEMBLY_TEST=1 python3 -B docs/evidence/A01/A01-24/mysql_redis_smtp_http_smoke.py`：退出 0；使用合成邮箱、本机 TLS SMTP、私有 MySQL/Redis 验证邮件挑战、新设备待批准、已有设备批准、同键重放、限流和训练接口回归；另在独立 schema 测试实际 `buildBusinessHandler` 装配、共享 epoch 及可信代理边界。第一次运行因新加一封合成邮件而旧测试仍期望两次 SMTP 连接失败，修正期望后复测通过；失败数据目录保留在本机临时目录。
- `go test -race -count=1 -run 'TestLoadAPISettingsProtectedFile|TestAPISettingsRejectAmbiguityAndInvalidPolicy|TestBusinessKeysRejectMissingReusedAndDisclosure' -v ./cmd/api`：退出 0，验证新增必填配置、窗口与上限约束。
- `python3 -B docs/evidence/A01/api-process/mysql_redis_smoke.py`：退出 0，使用私有 MySQL/Redis 验证含新配置的受保护进程启动、回环路由、代理边界和干净关闭；该进程测试没有覆盖真实部署。

这些是本机合成数据验证。新设备批准后的安全兑换、近期重新认证的产品流程、客户端批准页面、真实邮箱和代理、线上部署、双设备同步、安装包与真机均未完成或未验证；不能把本接口作为完整跨设备登录。
