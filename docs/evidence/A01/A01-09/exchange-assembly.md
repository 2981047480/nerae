# 新设备审批兑换的本机 API 装配

2026-11-03：受保护 API 的业务装配连接审批兑换、状态快照、独立 Redis 限流及 `/auth/login-approvals/{id}/exchange`。启动事务在已有审批 epoch 后校验兑换回执与状态快照 epoch；缺表、缺密钥或 epoch 不匹配时不返回 handler。兑换复用设备证明时效策略，但使用独立回执密钥和限流预算。

受保护配置新增必填字段：`approval_exchange_idempotency_key`、`approval_exchange_request_key`、`approval_exchange_response_key`、`approval_exchange_key_id`、`approval_exchange_receipt_seconds`、`rate_approval_exchange_window_seconds`、`rate_approval_exchange_capability_limit`、`rate_approval_exchange_ip_limit`、`rate_approval_exchange_global_limit`。三个密钥必须分别独立生成，且不能与现有业务或代理密钥复用；所有字段无默认值。此变更要求旧配置补齐后才可启动，生产值、密钥保管和轮换均待部署前确定。

验证：`NERAE_RUN_API_ASSEMBLY_TEST=1 python3 -B docs/evidence/A01/A01-24/mysql_redis_smtp_http_smoke.py` 在本机授权隔离环境 exit 0，使用私有 socket MySQL／Redis、合成账户及设备、Go race 测试。从同一受保护业务 handler 经过可信代理边界完成新设备待批准、旧设备批准、待批准快照重试、新设备领取同主体会话及令牌精确重放；第一设备创建训练，第二设备读取并保存一组，第一设备可读取。测试另验证错误来源、缺额度、回执密钥不匹配和启动事务回滚。验证码由测试派生，没有使用 QQ 发信。

`python3 -B docs/evidence/A01/api-process/mysql_redis_smoke.py` 在本机授权隔离环境 exit 0：实际 `run` 开启回环 listener，检查代理凭据与可信来源头，并确认新兑换路由对无效请求返回有界错误后干净关闭。该进程测试未在网络 listener 上跑完整批准成功路径。`go test -race -count=1 ./cmd/api` 的普通运行也通过，但未设置私有服务时数据库集成测试按设计跳过。独立只读复核未发现明确 P0/P1；未验证线上 HTTPS 代理的真实转发、真实邮箱与双真机。

限制：过期回执清理、跨机时钟偏差、客户端审批轮询和设备同步未完成，不能据此发布或声称 App 已可用。会话提交后若来源授权撤销或原证明过期，重取可能失败而已提交会话仍保留。
