# A04-09 HTTP 写入隔离验证记录

## 范围

在真实本机 TLS `httptest` server 上组合注册 `NewWriteHandler` 与既有 `NewReadHandler`，使用真实 challenge/device proof/session 流程签发合成主体 token，并连接仅供本次测试新建的 socket-only MySQL。未挂载公网路由、未使用真实邮箱或外部服务。测试运行 `transport` 包，不代表真实用户登录、公开 HTTP 部署或端到端客户端产品验收。

## 验证

工作树基线 HEAD：`82b688a1e368b32d12631d8497bf902c7ebf9e5d`。

- `cd apps/api && GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOCACHE=/private/tmp/nerae-go-api-cache GOMODCACHE=/private/tmp/nerae-go-api-mod /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race -count=1 ./internal/modules/workout/transport` — exit 0；无集成 DSN 时 DB 测试按 fixture 约定跳过，提供编译/race 基线。
- `python3 -B docs/evidence/A04/A04-09/mysql_smoke.py`（harness 显式设置 `NERAE_WORKOUT_WRITE_CLIENT_INTEROP=1`）— 最终 exit 0。`TestWorkoutHTTPRead`、`TestWorkoutHTTPWrite` 均 PASS，输出包括 `PASS: actual client adapter created, saved, replayed, finished and restored over isolated TLS HTTP`；脚本确认 MySQL 正常关闭并只清理本次新建目录。开发中首次 sandbox 内 MySQL 初始化因 `mysqld` SIGSEGV 退出 1；获准外部运行后一次因测试 fixture auth epoch 不一致退出 1并保留失败目录，统一为既有 read fixture 的合成 auth keys 后复跑通过。
- `/private/tmp/nerae-go-toolchain-1.27.3/go/bin/gofmt -d apps/api/internal/modules/workout/transport/write_test.go` — exit 0，无格式差异。
- `git diff --check -- apps/api/internal/modules/workout/transport/write_test.go docs/evidence/A04/A04-09` — exit 0；另行检查两个新文件无行尾空白。

HTTP 测试覆盖缺 Bearer 401、未声明成年 403、free 创建、集合写入/完成及读回 schema、相同幂等键精确重放、同主体相同键不同正文 409、旧版本 412 的重放正文与 `X-Request-ID` 一致、另一主体复用键时无正文泄露、非法 JSON/重复 JSON 键/超精度/错误顺序 422、合法但尚不支持的 plan/catalog/finish 可选字段不得静默成功、`Cache-Control: no-store` 和成功幂等到期头。另以独立 free 记录验证 `expectedVersion:1e0`、`setIndex:1.0`、`weightKg:4.250e1`、`reps:8e0`、`rpe:75e-1` 精确解析，且省略输入 `completed` 仍返回已确认组；包括 `42.500`、`4.2500e1` 的尾零三位精度、其他超精度、非整数、uint64 溢出及极大指数均返回 422，并逐项读回确认没有写入。定向缓存单测确认 schema 缺字段的旧成功正文被替换为不泄漏原文的通用 503，合法缓存正文仍逐字节原样返回。

## 限制

- 启用互操作标志时，Go 测试在 TLS server 存活期间启动 Node 26 执行 `client_write_interop.mjs`；测试证书写入 `t.TempDir()`，信任通过 `NODE_EXTRA_CA_CERTS`，URL 与三个合成 token 仅通过子进程环境传递，不落盘且失败输出会脱敏。实际验证的是 TS 客户端 adapter，不是已安装 App。
- 未验证公网路由挂载、真实邮箱/设备、生产数据库/密钥、plan/catalog 写入和额外 finish 字段；这些受限路径明确返回安全错误，不构成支持声明。
- 未运行 App 原生包、真机或业务发布验收。

## Lead 复核与同版复跑

Lead 将实际客户端互操作补充为两条路径：逐组各自保存，以及训练后在一次批次中保存多个动作和组；两者完成后由同主体另一合成会话读取恢复。`python3 -B docs/evidence/A04/A04-09/mysql_smoke.py` 在当前候选源码退出 0，包含真实 Go HTTP/TLS、私有 MySQL、Node RecordingApi 和 race 测试。两次会话使用同一合成设备密钥，不代表真实双设备批准。

独立复核发现缓存重放前未按当前响应 schema 复验；`writeResult` 已在不改变原始回执字节的前提下重新校验，缺少必填项返回安全 503，测试通过。数字按有界 `big.Rat` 精确解析，不经 float64；整数可接受 `1.0`/`1e0`，`4.250e1` 的两位有效小数可写入，`42.500`、`4.2500e1`、超精度、溢出及过大指数拒绝且不写入。数字 token 上限 128 字节、指数范围 ±128，是接口资源边界，超出时返回 422。计划记录、动作库和完成可选字段仍明确失败，不可视作完整合同实现。

源码 SHA-256：

- `write.go`：`a7aba56191bea9b1d328003023fef6c8bcfece8c0e8bc27ec058a64adcb7b012`
- `write_test.go`：`888bbf3b21145049ed6ab284e98178830c76c0a42d9db601620ff6d5bfa186b4`
- `mysql_smoke.py`：`90b14d61c5d4e6d7ed21b33cbcaf60a1351247a91c6ab6b3c97ff4de4b7f7e37`
- `client_write_interop.mjs`：`0056437c3dbf677f8a62ea528af7556c9a6fcb73b42daa3fa5396ff837b5c00b`

2026-10-30 在集成版本 `00e5da9` 复跑 `python3 -B docs/evidence/A04/A04-09/mysql_smoke.py`，退出 0；私有 MySQL 与本机 TLS 下真实客户端适配器通过训练后批量记录、逐组记录、同键重放、完成及合成会话恢复。测试主体与设备仍为合成数据，恢复使用相同模拟设备密钥，不代表真实登录、两台设备同步或正式页面已接通。

2026-10-31 在 RecordingApi 幂等回执候选树复跑 `python3 -B docs/evidence/A04/A04-09/mysql_smoke.py`，退出 0。Node bridge 只从真实 TLS `fetch` 响应读 `Idempotency-Key-Expires-At`，传递此白名单头，不合成或填默认值；互操作明确断言创建、组写、完成及同键重放都收到服务端有效期原文，GET 结果没有写入回执。私有 socket-only MySQL 成功后由 harness 正常关闭并清理本次 fixture。此为本机合成账户/会话与 TLS HTTP 联测，不代表安装 App、真实双设备或持久重放队列。
