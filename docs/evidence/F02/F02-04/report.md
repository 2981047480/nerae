# F02-04 已认证写入幂等基础：本轮证据

状态：数据库事务基础已实现并通过隔离验证，任务仍为 active；当前没有任何真实训练写入 HTTP 端点使用它。基线 `478053f`。Lead 负责设计、实现和集成；`/root/contract_tests` 在独立写域补充测试，`/root/foundation_review` 两轮只读审查并定向复审。最终复审未发现 P0–P2 阻断，测试结果由下述实际运行提供。

- 认证主体从当前数据库会话派生；同一事务持有授权与幂等行锁，业务变更和加密响应原子提交。请求 JSON 拒绝重复键并规范化对象顺序，摘要使用服务端 HMAC；响应 AES-GCM 加密，附加认证数据绑定主体、方法、具体路径、键、摘要、状态、资源引用和到期时间。
- 同键不同请求冲突；相同请求重放之前重新验证会话、年龄和资源权限。调用方须锁住可变授权行直到提交。受限事务视图不提供提交/回滚，只允许调用方在启动时注册的固定 SQL；这不是通用 SQL 安全沙箱，生产账号仍须最小权限，白名单和每个接入点须单独审查。
- 已确认的业务 409/412/422 由接入方显式返回 Reply，执行器回滚业务 savepoint 后加密保存失败结果；瞬时错误整体回滚。过期清理为显式调用的有界维护方法，仅删除终态，不删除 processing。保留期由配置显式提供，未将待确认的 24 小时写为默认值。

验证：

- `python3 -B docs/evidence/F02/F02-04/mysql_smoke.py`：退出 0，私有 Unix socket MySQL、合成会话和业务数据；独立测试角色与 Lead 分别运行通过。覆盖真实行锁竞争、同键只写一次、不同请求冲突、主体/方法/路径/键隔离、回滚后重试、已提交响应丢失后恢复、权限行并发撤销、失权不返回缓存正文、409/412/422 失败回执重放、UTF-8 键边界、密文与 HMAC、SQL 白名单、过期清理与过期键并发刷新。成功运行只删除本次临时目录。
- `go run ../../scripts/dev/migrate.go check`：退出 0，清单校验 11 项迁移；`go test -race -count=1 ../../scripts/dev/migrate.go ../../scripts/dev/migrate_test.go`：退出 0。新迁移已串行登记全局清单；没有对用户服务器执行迁移。
- 早期测试夹具曾出现非法 JSON、作用域变量及全表清理顺序错误；提交前差异检查还发现 harness 末尾多余空行，已移除，修正测试后通过；失败运行的私有临时目录按 harness 约定保留。上述“响应丢失”模拟的是已提交后调用方未收到结果，不是对数据库 COMMIT 返回未知的故障注入。

被测文件 SHA-256：

- `canonical.go`：`453567109e6e7bb0b3a49b4eb133a5ed8dcd86bf7130f3796b77e19cc9532221`
- `store.go`：`b2c179d23e895a103922ce05faece72716605a1b5eb57bb966f4e6488fa9ee95`
- `transaction.go`：`fca0f1c7f2517b9b1b77053976702ecf4551d0a4c016a0cc5b73798f75ac79db`
- `store_test.go`：`8ef54cc67a131830ac607a303c5e5f805320b35be4703a697f266bc630636a53`
- `0001_idempotency.up.sql`：`5d9d7bf7212434406bffe844c90e87ff473c4e7d8a8f8340833f0c1846950c64`
- `scripts/dev/migrations.json`：`a8a96d2a159cd4b5661d881fdabd43b8560eb760f2bfd93fe43e4a69be17ed45`
- `mysql_smoke.py`：`9da3c102c1a8f3637fad869ba81973b167029384fdfaf5501877dda6f9b4eff8`

训练仓储当前自行接收 `*sql.Tx` 并由服务管理提交，尚未适配受限事务；因此用户的两种训练记录写入还没有通过此幂等执行器。匿名邮箱挑战、验证码/设备凭据响应重放、SMTP outbox、Redis、保留期运营决策、密钥轮换、真实事务提交未知故障注入及生产调度仍待独立完成。不能将本卡当作 F02-03 全卡验收或真实 App 跨端同步。

## 事务能力共享接缝

基线 `73fef01` 新增 `internal/dbtx` 窄接口和普通 SQL 事务适配器，幂等受限视图改用相同接口类型；普通适配器不提供 SQL 白名单，不能代替幂等执行器的受限视图。`Wrap(nil)` 返回 nil，调用方须检查；其他情况要求有效活动事务。Lead 独立审查并由 `/root/foundation_review` 只读复核，无 P0–P2 发现。

`go test -race -count=1 ./internal/dbtx ./internal/idempotency` 退出 0；`python3 -B docs/evidence/F02/F02-04/mysql_smoke.py` 退出 0，隔离 MySQL 的并发和授权测试再次通过。源文件修改仅增加说明注释后重新计算下列哈希；前节哈希描述初次集成版本。训练仓储尚未切换，需在接入后同版重测。

- `dbtx/transaction.go`：`88e15f94a554914690d6ec60dbe419e940685c8782a2d6bc38fd97eedee0f61b`
- `dbtx/transaction_test.go`：`4328335675da474d18299bef9d267da9d2d765fdc4b79d1ace7fb9d5b12a3f06`
- `idempotency/transaction.go`：`a06f00f1f0504bd028ce3f3174640225d60dcf1208d52cb7c0bb19c3a69488e2`
