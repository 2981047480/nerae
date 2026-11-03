# 新设备审批查询的匿名限流

2026-11-03，新增独立审批查询限流。入口先对可信来源 IP 和全局预算计数，再对申请 ID 与 secret 的 HMAC 组合计数，最后才可访问数据库。只有申请 ID 而没有正确 secret 的请求不会耗尽真实申请的专用预算；所有请求仍消耗入口预算。Redis 不保存原始申请 ID、secret 或来源地址，限流器故障时失败关闭。

`RateLimitedPoll.Begin` 返回不可由外部实现或复制内部状态的一次性入口；未来 HTTP 处理器须在解析请求体之前调用它，并从可信代理边界获取来源 IP。状态、加密回执和旧兑换的无额度方法均改为包内私有；此前事务接缝证据里提到的公开 `Exchange` 是当时的接口，现已收回。

验证：`python3 -B docs/evidence/A01/A01-14/redis_smoke.py` 使用隔离 Redis 和 Go race 测试，覆盖独立用途、IP/全局/申请限额、不同 secret 隔离、并发上限、隐藏原始标识及 Redis 故障。`go test -race -count=1 ./internal/modules/auth/approval -run '^(TestRateLimitedPollOrdersEveryRequestBeforeDatabase|TestPollEntryCannotBeReusedOrCopied|TestOnlyRateLimitedPollExportsExchangeEntry)$'` 使用可控测试端口验证调用顺序、错误阻断和 16 次并发复用仅一次通过；它本身不是 MySQL+Redis 端到端测试。

限制：尚未装配到 HTTP 或实际 API 启动配置，尚未在同一进程使用真实 MySQL 和 Redis 验证本入口，也未确定上线限额、状态回执清理、代理配置或真实设备行为。该证据不代表线上登录或跨设备同步已完成。
