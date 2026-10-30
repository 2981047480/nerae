# A01-19 邮箱队列后台执行循环

## 状态

实现已集成至 `ad727bb`，同版本 `python3 -B docs/evidence/A01/A01-19/mysql_smoke.py` 退出码 0。任务图仍须按卡要求保持 `active`：真实 SMTP 配置、生产装配/部署及收件人邮箱送达没有验证。

## 变更

- `apps/api/internal/modules/auth/mailworker/worker.go`：提供 `New`、`Run`、`RunOnce`。未显式提供 Outbox、数据库和有效轮询配置时拒绝构造；没有 init 或自动启动。每轮先调用 `RecoverExpiredSending`，随后最多调用一次 `SendOne`。空队列正常空闲；只接受 Outbox 定义的终态；未知状态按错误处理。数据库/outbox 错误只返回固定分类，循环日志不记录底层错误。
- `apps/api/internal/modules/auth/mailworker/worker_test.go`：覆盖恢复优先、每轮单次发送、空队列、unknown 不重试、恢复失败不发送、错误脱敏、启动取消和缺省配置拒绝。
- `apps/api/internal/modules/auth/mailworker/worker_integration_test.go`：使用真实 challenge、Outbox、SMTPMailer 和 Worker。向本机 TLS sink 提交第一封并验证 `accepted`；第二封在收到 DATA 后关闭连接，验证 `unknown`；新建 Worker 模拟重启后确认 unknown 状态不变且没有重复 SMTP 连接。sink 持续接受新连接直到关闭，并用原子计数确认重启后的总连接数仍为 2。测试限定 harness 私有路径、schema、root 用户、空密码、UTC、私有父目录和非符号链接 Unix socket。
- `docs/evidence/A01/A01-19/mysql_smoke.py`：每次新建权限为私有的 MySQL datadir，禁用网络监听，仅开放 Unix socket；迁移三份互相隔离的测试 schema，运行带 race 的 Worker、Outbox 和 SMTP 测试，成功后关闭并删除临时库。

没有修改 worker 入口、配置、迁移、OpenAPI、其他模块或共享文件。未初始化或重置任何 key epoch。

## 验证证据

- `pwd && git rev-parse --show-toplevel && git rev-parse --short HEAD && git status --short --branch`：工作树为 `/Users/zephyr/.codex/worktrees/nerae-mail-worker/nerae`，基线 `9d0f5c8`，开始时工作树干净；命令退出码 0。
- 使用 Go 1.27.3 与隔离 `GOMODCACHE`/`GOCACHE`。`gofmt -w` 完成；后续 `gofmt -d` 无差异。
- `python3 docs/evidence/A01/A01-19/mysql_smoke.py`：退出码 0。脚本新建三套私有 schema，设置专用 Unix socket DSN，在 loopback 绑定 SMTP sink；临时 CA 仅配置给测试进程，并设置 `GODEBUG=x509sslcertoverrideplatform=1`。没有关闭 TLS 链或主机名验证。
- harness 执行 `go test -race -count=1 -v ./internal/modules/auth/mailworker ./internal/modules/auth/issuance ./internal/modules/auth/mail`：三包退出码均为 0。Runner 集成真实经过 Worker→Outbox→MySQL→SMTPMailer→loopback TLS sink；Outbox 状态机、并发与 SMTP interoperability 回归通过。Worker 零值 `Run`/`RunOnce` 失败闭锁测试通过。Issuance 中依赖 Redis 的独立 request 测试按设计跳过。
- harness 成功后按脚本关闭临时 MySQL 并删除整个临时目录；未触碰已有测试库。
- `git diff --check`：退出码 0。

## 风险与后续

- 本卡合成集成证据不代表供应商 SMTP 账号、真实邮箱投递、生产配置、worker 进程装配或部署已经验收。
- 没有连接 QQ 或用户服务器。
