# A01-18 邮箱挑战 HTTP 接口内部装配

## 状态

`partial`。未挂载的 handler、严格输入/输出校验、错误映射及私有服务集成用例已写入。隔离工作树及集成工作树均以真实私有 MySQL/Redis 和本机 TLS 运行 race 通过；生产路由、真实邮箱和客户端接线尚未完成，因此本卡未验收。

## 范围与行为

- 基线：`8dc2541b7f966377032834996436d52ac037f326`。
- 仅新增 `apps/api/internal/modules/auth/transport/` 和本目录文件；未改生产路由、共享迁移、OpenAPI 或依赖。
- `NewEmailChallengeHandler` 需要实际数据库句柄、issuer、SchemaValidator 和显式可信来源 IP resolver。handler 不读取 `Forwarded` 或 `X-Forwarded-For`。
- 只接受 `POST /auth/email/challenges` 且无 query 的请求；检查唯一、规范 raw-base64url 高熵幂等键，使用合同 SchemaValidator 拒绝重复/额外 JSON 字段和非法 UTF-8，再对邮箱语法、公钥曲线及受信 IP 做调用前检查。
- 业务由 `ChallengeRequestIssuer` 执行；成功响应需有限长度、合法 UTF-8、无重复/额外 JSON 字段、有效挑战 ID/nonce/未来过期时间，并再次经 OpenAPI 响应 schema 校验。成功仅返回 `202`，不宣称邮件已投递。所有响应设 `Cache-Control: no-store`；错误正文由共用中间件生成，不带内部异常或请求数据。
- 测试文件包含拒绝路径单测和调用真实 issuer 的本机 TLS + 私有 MySQL/Redis 集成用例。集成 sender 是不发信的测试实现；验证 outbox 接受，不验证 SMTP/用户邮箱送达。

## 验证

| 命令 | 退出码 | 结果 |
|---|---:|---|
| `GOPROXY=off GOSUMDB=off GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -mod=readonly ./internal/modules/auth/transport`（在 `apps/api/`） | 0 | 包内测试通过；无私有服务环境变量时，集成测试按设计跳过 |
| 同上，增加 `-run TestEmailChallengeHandlerRealIssuerOverLocalTLS -v` | 0 | 明确显示集成测试因缺少私有 MySQL/Redis 环境而跳过，不是集成通过 |
| `python3 -B docs/evidence/A01/A01-18/mysql_redis_tls_smoke.py` | 2 | 运行时检测到 791,019,520 字节可用空间，低于脚本要求的 1,073,741,824；脚本在启动任何服务前退出，未创建测试目录 |

最初未运行 `-race` 或私有服务，因为可用空间不足。归档已完成工作树释放空间后，Lead 在任务工作树与集成工作树分别运行相同的 `python3 -B docs/evidence/A01/A01-18/mysql_redis_tls_smoke.py`，均退出 0：真实 issuer、私有 Unix socket MySQL/Redis 和本机 TLS 的 race 测试通过；每次仅删除该次成功运行创建的临时目录。

## 限制

- handler 未挂载生产 Gin 路由；可信代理解析策略由未来装配方显式提供。
- 上述隔离 MySQL/Redis 与 TLS 测试不代表正式服务部署、真实邮件投递或手机联调；失败现场按脚本保留规则处理。
- 不发送邮件、不连接用户数据库或外网；测试 sender 不等于 SMTP 验收。

## 当前文件 SHA-256

```text
afc414ee1d91fb0cc6172f734b30ce1379928ff2cb959dc72b5b2a15d3eb369d  apps/api/internal/modules/auth/transport/email_challenge.go
c4f19a102c84e7fec2bfa0fdeea51d25c02b80d7be13c9f4b1cdeaf312476be7  apps/api/internal/modules/auth/transport/email_challenge_test.go
99bdf08abb064d47992837b5cdc2aa2581cd000c5031990d27d818d3879d335c  apps/api/internal/modules/auth/transport/email_challenge_integration_test.go
03577c246b7735500b19ee7f5773643fc9afda555b4977ecb7559773e4f27452  docs/evidence/A01/A01-18/mysql_redis_tls_smoke.py
```
