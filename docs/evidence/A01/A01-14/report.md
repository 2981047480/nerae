# A01-14 邮箱认证限流基础验证

## 结果

status: success（有界限流基础及隔离 Redis 验证完成；不是认证 HTTP、真实登录或上线接受）。

基线：`e7c54e7310b2ee083b6de1601e8de80caae57609`，工作树原始状态 clean。A01-14 任务卡从集成树核对；本基线内尚未包含该卡。依赖沿用 `apps/api/go.mod` 中锁定的 `github.com/redis/go-redis/v9 v9.23.0`，未改依赖、路由或配置。

## 实现边界

- 新包 `apps/api/internal/modules/auth/ratelimit` 提供 `email_challenge` 与 `email_login` 两类用途，各自显式配置 email/device/IP/global 四维限额和固定窗口。
- 构造器要求四把互异、非零、32 字节 HMAC key；内部复制密钥。邮件索引和设备指纹使用固定 32 字节值，网络来源必须由调用方在可信边界解析为 `netip.Addr`。不读取或解析转发头；拒绝无效、未指定、多播及带 zone 的地址，IPv4 固定按主机 `/32` 计数，IPv4-mapped IPv6 先归一化；IPv6 前缀长度显式配置。
- 四个键分别按维度独立 HMAC；用途、维度、载荷以长度前缀分隔。键中不含原始邮箱、IP 或设备指纹，并共享固定 Redis Cluster hash tag。
- Lua 脚本先检查四个计数键的类型、整数格式、计数边界及 PTTL；任何一维超限或状态异常都在写入前返回。全部未超限时，缺失计数用 `SET ... PX` 创建，已有计数仅 `INCR`、不续期。所有 Redis 错误被映射到通用 `ErrUnavailable`；限流结果不暴露具体维度。输入、配置、Limiter 的 String/GoString/JSON/slog 输出均脱敏。
- 配置限额范围为 1–1,000,000；窗口范围为 100ms–24h，须为整毫秒；IPv6 前缀为 /16–/128。IPv4 的 `/32` 固定，不提供可调前缀。模块不选择生产限额或读取密钥配置。

Lua 在并发隔离下先检查全部四个键；超限、错误类型/计数/TTL 的预检结果在任何写命令前返回。Redis Lua 执行期间其他客户端不能交错观察中间步骤，但脚本不是回滚事务：如果四条写命令中的后续命令发生运行期错误（例如 Redis OOM），Redis 不保证撤销此前已完成的写入。本次验证覆盖预检拒绝与坏状态无部分写入，未注入多键写入期间的运行期错误。

## 验证

- `cd apps/api && GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOCACHE=/private/tmp/nerae-go-api-cache GOMODCACHE=/private/tmp/nerae-go-api-mod /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race -count=1 ./internal/modules/auth/ratelimit` — exit 0。无 `NERAE_RATELIMIT_REDIS_SOCKET` 时只执行单测，真实 Redis 测试按环境约定跳过。
- `python3 -B docs/evidence/A01/A01-14/redis_smoke.py` — exit 0。脚本用 `/private/tmp/nerae-runtime-artifacts/redis-src/src/redis-server`（本机显示 Redis 7.4.12）启动本次专用 Unix socket Redis，`--port 0`、`--save ""`、`--appendonly no`，无 TCP listener、无持久化；设置环境变量后执行 Go race 集成测试。最终日志通过，子进程正常退出并仅删除本次成功的临时目录。
- 集成测试验证两种 purpose 相互隔离、四维分别限额、IPv4 `/32` 主机计数及 IPv4-mapped 归一化、IPv6 `/64` 聚合、固定窗口过期、100 个并发请求下 global cap 恰为 17、限额拒绝不部分消耗其他维度、persistent TTL/超窗口 TTL/非法计数/错误 Redis 类型失败关闭且无其他键写入、Redis client 已关闭时返回脱敏 unavailable。
- `/private/tmp/nerae-go-toolchain-1.27.3/go/bin/gofmt -d .../ratelimit.go .../ratelimit_test.go` — exit 0；`git diff --check` — exit 0；Python AST 解析和行尾空白检查通过。

一次早期 harness 运行时测试仍按原 `/24` 假设构造 no-partial 探针，两个探针地址因此合法共享当时的 IP key，断言误触发 IP 限额，退出 1并按脚本策略保留失败目录 `/private/tmp/nerae-email-ratelimit-dnleqnsl`。随后先调整探针，再将最终 IPv4 语义固定为 `/32` 并同步重测。该失败是历史夹具误判，不是限流器结果；保留目录未触碰。

## 未验证与后续

- 仅在本机 Redis 7.4.12 standalone Unix socket 实测；固定相同 hash tag 的 Cluster slot 设计已编码，但未连接或验证真实 Redis Cluster、多实例配置、网络故障切换或部署拓扑。
- HMAC 密钥必须由部署方使用 CSPRNG 独立生成、跨实例一致并按环境隔离。代码只能校验长度、非零及互异，不能证明熵；密钥轮换会改变计数键，需 Lead 设计协调切换。
- 真实生产限额、窗口、IPv6 前缀与可信代理边界尚未确定。上层必须先验证代理来源，再传入 `SourceIP`；不能把客户端 X-Forwarded-For 直接传入。
- 尚未接入邮箱 challenge/login 应用服务或 HTTP；本包不处理已存在的 challenge 重放顺序、匿名请求前置校验、SMTP、账号、认证或最终授权。没有真实发信或登录验收。
- 当前执行环境未向 worker 暴露可核实的具体模型路由/推理参数，报告不猜测；由 Lead 的任务登记记录为准。需 Lead 独立安全 review，并在集成树同版复跑后决定接受。
