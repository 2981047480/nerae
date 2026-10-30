# A04-03 · 动作顺序事务能力适配

状态：partial，仅完成排序仓储的事务能力接线，不代表 A04-03 整体接受。

保留 `ReadOrderedFree(..., *sql.Tx, ...)` 与 `ApplyOrderedBatch(..., *sql.Tx, ...)` 原导出入口，改为兼容包装；新增 `ReadOrderedFreeTx(..., dbtx.ReadTransaction, ...)` 和 `ApplyOrderedBatchTx(..., dbtx.Transaction, ...)`。读方法沿用 `ReadFreeTx` 按 subject 过滤并锁定会话、密钥 epoch 等已有数据，再通过 `loadTx` 与领域校验验证动作顺序。写方法使用调用者事务锁定用户、会话和密钥 epoch，读取并校验旧顺序，再调用 `applyBatchWithoutOrderTx`，最后在同一事务更新完整顺序；不自行提交，也不替调用方执行认证、年龄或资源授权。

order.go 的本文件 SQL 均改为固定常量。`OrderStatements()` 返回新建的 reads/writes 切片，仅含 order.go 直接执行的 4 条读 SQL 和 3 条写 SQL；调用方还须合并 `RecordStatements()`、`FreeDetailStatements()`，并处理重复语句。测试覆盖清单分类/复制、nil capability、DBTX 读写、真实 MySQL 版本冲突、无效目标顺序及旧 `*sql.Tx` 包装路径。

验证：

- `GOTOOLCHAIN=local GOPROXY=off GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race -count=1 ./internal/modules/workout/repository`：退出 0；未设置测试 DSN，隔离数据库测试按设计跳过。
- `python3 -B docs/evidence/A04/A04-03/order_smoke.py`：授权环境退出 0。新建私有 socket-only MySQL，应用含 `0003_workout_exercise_order` 的迁移、执行旧无序数据迁移拒绝反例，并运行完整 repository race 测试包；成功后仅清理本次测试目录。
- `git diff --check`：退出 0；order.go 无内联 `Query/ExecContext(ctx, \`SQL\`)` 调用。

限制：新 capability 入口仍由 `dbtx.Wrap` 在此测试中驱动，尚未在真正受限的幂等 `Mutate` 回调中接线；事务内授权与白名单合并由后续集成验证。本任务不提交、不修改任务状态。

文件 SHA-256：

- `apps/api/internal/modules/workout/repository/order.go`：`188abc18fcded9e8958cad2afadd8b2a8e1281c03e5d0634954753555639efd5`
- `apps/api/internal/modules/workout/repository/order_test.go`：`5a883036bfea2caaee589373fd397c60a5fb048c116c4b6c1593b9c6afd7ea77`
