# A01-11 外层事务组合接缝

状态：代码、轻量包验证及隔离 MySQL/race 均通过。此接缝不实现 HTTP、幂等、限流、SMTP 或完整真实登录。

基线：`bb4c5ad1a5d993120df283d5e6de2190be9b4d3b`。

改动范围仅为 `apps/api/internal/modules/auth/application/email_login.go`、`email_login_test.go` 与本报告。旧 `VerifyEmail` 调用签名保留并委托事务内方法。新 `VerifyEmailInTransaction` 不提交、不回滚；结果通过 `Kind` 和 `CommitRequired` 告知外层错误码尝试需提交。基础设施或一致性错误返回不可用，调用方应回滚。成功结果仅携带私有 opaque session/approval capability；`AfterCommit` 委托现有能力在原事务结束后开新事务重新授权，按类型释放凭据。对 `CommitRequired` 的拒绝结果，`AfterCommit` 在源事务仍活动时返回不可用，结束后才返回通用拒绝。`database/sql` 的 `sql.Tx` 只能表明事务已结束，不能区分提交和回滚；外层必须且只能在 `Commit()` 返回 nil 后调用该结果并据此认为错误次数已持久化。调用方若回滚后仍调用，方法无法判断且只返回通用拒绝，不代表计数落库。

隔离 MySQL/race 场景：错误码外层提交与回滚、活动事务期间拒绝提前返回、回滚后错误计数仍为零、session 与新设备 pending 分支互斥、短 TTL 审批过期、身份撤销、短 TTL token 过期、审批 epoch 错误回滚。测试复用既有 MySQL harness 的 identity、approval 与 proof-replay epoch，避免共享单行 epoch 冲突。审批和 session 过期均通过真实短 TTL 与等待验证，没有修改存储时间戳。

已运行：

- `/private/tmp/nerae-go-toolchain-1.27.3/go/bin/gofmt -w apps/api/internal/modules/auth/application/email_login.go apps/api/internal/modules/auth/application/email_login_test.go`，退出 0。
- `env GOPROXY=off GOSUMDB=off GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -mod=readonly ./internal/modules/auth/application`（cwd `apps/api`），退出 0。该次没有设置隔离 DSN，MySQL 集成测试跳过，仅确认包编译及可运行测试通过。
- `git diff --check`，退出 0。
- `python3 -B docs/evidence/A01/A01-11/mysql_smoke.py`，退出 0。该 harness 以 Unix socket 初始化全新私有 MySQL，应用其既有隔离迁移，运行 `go test -race -count=1 -v ./internal/modules/auth/application`。`TestEmailLoginTransaction`、`TestEmailLoginRejectsMissingDependencies`、`TestEmailLoginTransactionSeam` 均通过；成功后脚本关闭数据库并删除本次目录。
- 本轮补丁后的同一命令再次退出 0；新增 MySQL 反例在事务仍活动时拒绝提前返回错误结果，外层回滚后数据库 attempts 仍为 0。该反例也说明已结束的 `sql.Tx` 无法证明 commit：调用方只能在 `Commit()==nil` 后将错误计数视为持久化。

数据库执行前 `/private/tmp` 可用空间为 5.1 GiB，首轮成功后约 4.8 GiB；本次回归前为 4.6 GiB。第一次普通沙箱 MySQL 初始化因 `mysqld` 报 `Invalid permissions for mapped object` 崩溃，保留 `/private/tmp/nerae-email-flow-mysql-owy8ijky`；随后授权的集成运行发现测试 replay epoch 与既有测试不同，修正为共享测试 epoch；再运行发现用 SQL 直接回拨审批时间违反 DDL 检查约束，改为 1 秒 TTL 并真实等待。最终成功目录由 harness 按规则自行删除，失败目录均保留。未验证范围包括 HTTP/幂等/限流、SMTP、真实身份供应商、真实设备及完整公共认证链路；数据库 race 结果仅覆盖此合成事务接缝。

Lead 在集成工作树复跑 `python3 -B docs/evidence/A01/A01-11/mysql_smoke.py`，退出 0，原事务和新增外层事务接缝的私有 MySQL/race 测试均通过；该成功只覆盖本机合成场景，不包含匿名登录幂等或真实设备。
