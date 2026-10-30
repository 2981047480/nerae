# A01-14 登录挑战限流前置接缝

## 结果

在基线 `939487db54eeb0dc33e6b7331575a027454952c7` 上新增未挂载的两阶段前置限流能力。`AllowLoginEntry` 必须先于任何 challenge 数据库读取，由全部登录请求共享同一 EmailLogin IP/global 配额。入口通过后，调用方才读取挑战；若找到挑战，读取服务返回脱敏 opaque metadata，调用 `ApplyLoginDimensionLimit` 才会再扣 email/device 维度。邮箱或设备额度超限时入口额度仍已消耗，这是保护数据库的预期行为。未知挑战不创建 email/device 键。

挑战存储可从 `challengeId` 读取服务端保存的固定长度邮箱索引和设备指纹；方法校验身份 key epoch、记录 key ID 与当前持久身份保护器一致，并验证受保护邮箱密文可由该保护器打开。metadata 不暴露键字段，只能通过窄的 limiter 接口传给 email/device 限流方法；格式化、JSON 与 slog 输出均脱敏。它不验证设备证明、不授予身份/权限，也不返回邮箱明文。未知挑战返回零值和 `found=false`，不暴露资料；损坏记录、epoch 或密钥不匹配失败关闭。

两阶段的 IP/global 与 email/device 分别原子检查并扣减。已知请求两阶段四个 Redis 键与兼容的 `Limiter.Allow` 完全一致；四维 `Allow` 保留旧调用兼容，但同一个新式登录请求不可把它和入口/身份阶段混用，否则会重复扣 IP/global。旧 `AllowUnknownChallenge` 保留为入口阶段兼容别名。Lua 先检查该阶段所有计数和 TTL，再增加，因此超限或损坏计数器不会在该阶段部分增加其他维度。IP 规范化与校验沿用现有逻辑，包括 IPv4-mapped 地址归一化、配置 IPv6 前缀、拒绝未指定/多播/带 zone 地址。

本任务未接入 HTTP、ReceiptLogin、生产路由或部署。未来 coordinator 必须先调用 `AllowLoginEntry`，入口限流通过后才能读取数据库；随后对已知挑战应用 opaque metadata 的 email/device 阶段，并在登录事务内重新验证挑战和授权。本任务没有 coordinator/HTTP，因此没有直接测试超限阻断数据库访问。

## 验证

- `gofmt` 涉及的 4 个 Go 文件：退出码 0。
- `cd apps/api && GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race -count=1 ./internal/modules/auth/ratelimit ./internal/modules/auth/challenge`：退出码 0。
- `python3 -B docs/evidence/A01/A01-04/mysql_smoke.py`：退出码 0。私有 socket-only MySQL race 测试覆盖正确元数据、未知 challenge、畸形指纹、持久 key ID/密文不匹配、epoch 缺失，以及原 challenge 事务回归；脚本成功后清理本次临时目录。
- `python3 -B docs/evidence/A01/A01-14/redis_smoke.py`：退出码 0。私有 socket-only Redis race 测试覆盖已知/未知共用相同 IP/global 键、IPv6 前缀共享、混合并发总量上限、超限不部分增加、坏 IP/global 计数器不部分写入、非法地址和 Redis 不可用失败关闭；脚本成功后关闭并清理本次临时实例。
- 两阶段键逐项与旧四维 `Allow` 对照；入口与已知 email/device 阶段分别验证 Redis 故障关闭、配额原子性和阶段间不重复扣费。入口超限阻止 MySQL 查询属于后续 coordinator/HTTP 集成验收项。
- `git diff --check`：退出码 0。

私有服务只在 `/private/tmp` 运行 Unix socket，无 TCP 监听、无持久化配置；未连接外部 Redis、MySQL、邮件服务或公网。两种数据库的集成测试分别覆盖 challenge 元数据和 Redis quota，并未做一个同时启动 MySQL 与 Redis 的端到端 HTTP 测试。

## 风险与边界

- 限流材料的读取仅是匿名保护措施，不是登录依据。HTTP 调用方必须对正常和未知 challenge 分别只执行对应的一条计数路径，避免同一个请求重复消费额度。
- Lua 在 Redis 内序列化执行并先做完整预检；Redis 不承诺脚本中后续写命令遇到运行期错误时回滚先前写入。本测试覆盖坏状态/超限预检不部分扣减，不注入多键写入阶段的 Redis OOM 或存储故障。
- 真实代理信任、生产阈值、生产 Redis 拓扑及多实例配置仍需 Lead 安全复核。该能力不能单独作为开放登录接口的接受证据。
