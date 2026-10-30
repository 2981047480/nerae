# A04-03 · 训练仓储事务适配增量

状态：partial，仅完成受限事务写入的机械接线基础，不代表 A04-03 整体接受。

保留原私有 `applyBatchWithoutOrder(..., *sql.Tx, ...)` 与 `load(..., *sql.Tx, ...)` 入口；新增 `applyBatchWithoutOrderTx(..., dbtx.Transaction, ...)` 和 `loadTx(..., dbtx.ReadTransaction, ...)`，旧入口经 `dbtx.Wrap` 调用共享实现。写入入口仍不做身份、年龄或资源授权，不拥有提交/回滚；调用方必须在同一可信事务中授权并在错误时回滚。`RecordStatements()` 返回固定 reads/writes SQL 切片副本，读取清单含 6 条 SELECT，写入清单含 7 条 INSERT/UPDATE/DELETE。records.go 的数据库操作均改为这些固定参数化语句；检查未发现残留内联 SQL 调用。测试覆盖清单分类/防篡改复制、nil capability 与旧入口拒绝，以及 MySQL 中首批写入走 DBTX 变体、后续旧入口仍工作。

验证：

- `GOTOOLCHAIN=local GOPROXY=off GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race -count=1 ./internal/modules/workout/repository`：退出 0；未设置 DSN，隔离数据库测试按设计跳过。
- `python3 -B docs/evidence/A04/A04-03/mysql_smoke.py`：普通沙箱退出 1，MySQL 8.4.13 在 datadir 初始化期间 SIGSEGV；授权环境退出 1，脚本加载了 `0001/0002` 但遗漏当前测试所需 `0003_workout_exercise_order`，导致 `TestOrderedWorkoutTransactions` 在 `order_test.go:85` 失败。两次失败目录均保留，未清理：沙箱初始化失败为 `/private/tmp/nerae-workout-session-mysql-9mtvcf6w`，授权环境遇到迁移缺失为 `/private/tmp/nerae-workout-session-mysql-rglmebc1`。
- 将 `order_smoke.py` 扩为应用 `0003` 后运行完整 repository 包：`python3 -B docs/evidence/A04/A04-03/order_smoke.py` 在授权环境退出 0。含旧无序数据迁移拒绝反例、完整 race 测试和成功后清理本次私有测试目录。
- `git diff --check`：退出 0。

未验证：该受限变体尚未由实际幂等 `Mutate` 回调调用；本次仍由兼容 `dbtx.Wrap` 在集成 MySQL 中验证其查询路径。幂等策略白名单如何引用 `RecordStatements()`、真实训练 HTTP 与同库授权/年龄事务由后续接线任务验证。本任务不提交、不修改任务状态。

文件 SHA-256：

- `apps/api/internal/modules/workout/repository/records.go`：`8faf054ed2d123e286b0985b6228a7a198581c97507ae3d0b92efdb201c108a8`
- `apps/api/internal/modules/workout/repository/records_test.go`：`382bb40645aff4f6f41c1dc76ecec8bb9a4b295577bc9e4814ef7ac119118b22`
- `docs/evidence/A04/A04-03/order_smoke.py`：`c7b1647e8c27018024228eb1e97ae0a76b57dbc605b02d5dca665190f1c762c3`
