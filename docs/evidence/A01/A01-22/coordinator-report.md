# 邮箱登录 application 两阶段协调器

## 结果

基线 `2550fe10aa15cdc05a46597331ca62e6d67d1dbd`。新增未挂载的 `EmailLoginCoordinator`，构造器要求真实 `*challenge.Store`、`*ratelimit.Limiter`、`*ReceiptLogin`，并拒绝 challenge store 与 ReceiptLogin 内部实例不一致。

`VerifyEmail` 的顺序固定为：先扣 `AllowLoginEntry` 的 IP/global 额度；通过后才开启短只读 MySQL 事务读取 `RateLimitDetails`，并提交结束该事务；未知 challenge 统一返回 `ErrRejected`；已知 challenge 通过 opaque metadata 调用 email/device 限流，然后开启独立登录事务调用 `ReceiptLogin.VerifyInTransaction`。无变化拒绝结果回滚；`CommitRequired` 结果提交，只有 `Commit()` 返回 nil 才调用 `AfterCommit`。提交错误或结果不确定时返回 `ErrUnavailable`，不释放 token/approval。`ErrRejected`、`ErrReceiptConflict`、`ratelimit.ErrLimited` 与不可用类别均保留；Redis unavailable 同时匹配 application 和 ratelimit unavailable sentinel，且没有原始依赖错误进入响应。

此协调器不解析 HTTP、请求头或日志，不连接生产路由；可信 IP 由调用方传入。四维兼容 API `Allow` 不与该两阶段协调器混用。

## 验证

- `cd apps/api && GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race -count=1 ./internal/modules/auth/application`：退出码 0。
- `python3 -B docs/evidence/A01/A01-22/mysql_redis_smoke.py`：退出码 0。新建私有 MySQL 8.4.13 与 Redis 7.4.12，仅 Unix socket；MySQL 禁用网络，Redis 使用 port 0、关闭持久化。Harness 按顺序载入 11 项既有迁移：auth 0001、0002、0003、0004、0005、0006、0009；user 0001；workout 0001、0002、0003。实际运行整个 `auth/application` race 套件，结束后关闭服务并删除本次专属目录。
- 集成测试覆盖：入口预算耗尽时以 nil DB 参数验证返回限流而无需触碰数据库；已知/未知 challenge；Redis 故障关闭；email/device 限流在登录事务前拒绝；错误验证码尝试与拒绝回执提交一次并可同键恢复；签名无效请求回滚且不改变 attempts/回执；成功令牌提交后释放、同键返回原令牌、变更请求冲突；新设备只返回同键稳定的待批准能力；两路相同请求并发只建立一个 session 并返回同一 token。
- 同键 429 恢复场景使用独立 limiter 与 `250ms` 固定窗口：首次请求成功后建立一个 session；立即以原始相同 key 和完全相同请求重试，得到 `ratelimit.ErrLimited`，session 仍为 1；等待窗口 `400ms` 后，在 1 分钟设备证明及 2 分钟回执仍有效时，同键重试恢复原 token，session 仍为 1。测试确认已确定的当前策略：同键重放也要通过入口与身份限流；被限流后保留原请求，窗口结束可恢复既有结果。
- `gofmt` 两个新增 Go 文件：退出码 0；`git diff --check`：退出码 0。

## 未验证与风险

- 无法通过当前 `*sql.DB` 协调器接口注入可控的 `Commit()` 返回错误；本次未伪造提交失败测试。代码路径仅确认 `Commit()` 非 nil 时立即返回 unavailable，不调用 `AfterCommit`。提交失败/结果不确定时不释放凭据的行为仍需后续可靠故障注入验证。
- 入口拒绝前完全不访问 DB 的验证采用已耗尽入口预算并传入 nil DB；这验证协调器先检查入口额度，但未使用 SQL driver query hook。
- 没有接入 HTTP、用户 DTO、真实代理信任或生产配置；不代表真实邮箱登录、线上服务或生产路由已完成。Redis Lua 多键写阶段的 OOM/运行期失败回滚也未注入。
- 一次早期 harness 运行因负例使用了结构不合法的 proof，受 `ReceiptLogin` 请求摘要校验映射为 unavailable，未通过；将夹具改为结构合法但由非挑战设备密钥签名的 proof 后，完整私有 MySQL/Redis race 套件退出 0。第一次失败目录按 harness 策略保留，未触碰。
