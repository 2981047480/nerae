# App 认证组合接缝 · 2026-10-31

## 范围

在集成树加入 App 端认证组合：受保护设备身份注册、原生设备签名、邮箱挑战与登录协调、系统安全会话存储、`/me` 校验及成年人自我声明适配器。`createAppAuthRuntime` 要求显式 HTTPS 服务地址，复用同一服务配置；不提供默认生产地址、明文存储或模拟网络回退。它尚未由正式页面调用。

登录请求结果未知时保留原正文和幂等键，仅由用户触发精确重试。可确认的验证码拒绝允许重新填写；不符合合同的 401 不作为确定拒绝。会话切换、退出和恢复开始时同步失效旧登录 guard 并清除协调器持有的临时请求与批准状态；自身安全存储写失败则保留原请求以供重试。传输仍在途时，异步调用栈或传输层可能保留其局部请求材料直到结束，并非取消请求。

年龄声明必须由用户明确确认并携带服务端版本；丢失响应或无法确认的 412 会刷新当前状态，但不自动重发写入。声明是 18+ 自我声明，不是身份或实际年龄核验。

## 验证

- 在 `apps/client` 目录，使用 `node --import ../../docs/evidence/P02/P02-11/node-ts-loader.mjs --test` 运行 `emailLoginFlow`、`ageAttestationApi`、`emailAuthApi`、`sessionCoordinator`、`meVerifier`、`deviceIdentityCoordinator` 以及 App 设备、会话和认证组合的测试：74/74 通过，退出 0。涉及的原生接口和 HTTP 回复均为合成端口；不证明手机、真实网络或 QQ 邮箱可用。
- `pnpm --filter @nerae/client run typecheck`：退出 0。
- `python3 scripts/quality/verify_uts_plugin_build.py`：退出 0；锁定版 uni-app 编译器 5.26 生成 App 的 JS 代理、iOS/Android UTS 包装源码和小程序的明确不支持回退。它不是签名安装包，也不证明真机执行。
- `NERAE_KEEP_NORMALIZE_PROBE=1 bash docs/evidence/P02/P02-12/verify-email-normalize-differential.sh`：退出 0；限定向量对照实际 Go 归一化实现，夹具保存在 `/private/tmp/nerae-p02-12-email-normalize.P9yNn90v`。客户端有意拒绝服务端目前不能稳定重复归一化的 IPv6 邮箱域字面量，不能说两端接受集完全相同。
- 集成提交 `97813ba` 上 `python3 -B docs/evidence/A01/A01-24/mysql_redis_smtp_http_smoke.py`：退出 0；私有 MySQL/Redis 与本地 TLS 合成 SMTP 接收器验证挑战、发送、登录、读取本人资料和版本化成年声明。`go test -race -count=1 -run '^TestAgeVerifyHandler' ./internal/modules/auth/transport` 使用本地 Go 1.27.3 与离线模块缓存退出 0。线上进程仍未挂载业务路由。
- 独立复核发现并修复登录 guard 的重入重复通知；构造 listener 内再次退出及取消其他 listener 的测试。退出期间已发送请求的取消机制仍待后续实现。

## 限制

无真实邮箱投递、已注册小程序账号、可信线上 HTTPS 地址、原生安装包或设备执行证据。新设备批准与安全兑换、恢复码、正式页面、完整可靠的训练待发队列和双设备验收尚未完成；应用进程结束后未完成的登录挑战需重新开始。

App 启动接线必须由同一个 runtime 统一建立会话单例；若其他调用方提前使用不同的 `/me` 读取端口初始化会话，runtime 会失败关闭，当前没有热切换或自动回滚流程。自我声明的布尔参数也不能证明来自用户操作，页面必须在明确确认后才调用。

## 关键源码 SHA-256

- `apps/client/src/features/auth/emailLoginFlow.ts`: `501c492f31dd69bded276a256c8be29c939adea6f2917af8965c15c23ff997e8`
- `apps/client/src/features/auth/sessionCoordinator.ts`: `61b63d868abae7b5480e1221759eae7d217304cdb390d158f4c5cd112c21b27e`
- `apps/client/src/features/auth/ageAttestationApi.ts`: `ca7a3a604dad997039dfbc50a4241b6d3d988bb553d458a70e37ea3aaf82686e`
- `apps/client/src/platform/app/nativeAuthRuntime.ts`: `97ac91143efdc21e1d959330df539b73d21b315a4298f36f78662a498dc1fffd`
