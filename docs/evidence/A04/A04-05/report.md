# A04-05 · 训练记录保存完成事务进度

状态：partial，未接受。Lead 基于 `04c5132` 增加自由训练记录的内部保存确认事务，经当前用户会话与年龄状态检查后，仅允许已有实际组的记录完成。服务端记录收到保存确认的时间，不创建训练开始、训练结束或推算总时长；同一记录的重复确认使用当前版本，不产生第二份记录。本卡没有实现完整的 HTTP finish 合同。

`python3 -B docs/evidence/A04/A04-05/mysql_smoke.py` 退出 0：在本机新建的私有 Unix socket MySQL 8.4 中运行合成账号、设备证明与数据，成功后关闭并移除本次专属目录。Go race 测试覆盖空记录拒绝完成、跨主体拒绝、同版本并发完成只有一笔成功、完成记录读回、重复确认不创建新记录、删除最后一组回草稿以及空草稿不能再次完成。测试实际持有身份行锁并观测两个请求等待；年龄标记仅为测试夹具，不是真实核验。独立复核建议覆盖完成写入后的授权失败，已补充同事务修改 token version 的故障测试，验证状态、确认时间与版本完整回滚，并重跑通过。

本次有序接线复核：仓储 `CompleteFree` 在更新前调用 `ReadOrderedFree`；应用公开 `CompleteFree` 委托现有同事务 `CompleteOrderedFree`；保存组测试明确传完整目标顺序，删除最后一组传非 nil 空数组。测试先删除隔离记录的顺序行，确认完成返回 `repository.ErrUnavailable`，再直接读取会话行断言版本仍为 2、状态仍为 `in_progress`、`recorded_at` 仍为空；随后仅恢复该测试会话的顺序行并继续既有完成/回归断言。测试迁移夹具现含 `workout/0003_workout_exercise_order.up.sql`。

`python3 -B docs/evidence/A04/A04-05/mysql_smoke.py` 退出 0：使用脚本创建的私有目录和 socket-only MySQL，运行 `TestWorkoutCompletionTransactions` 并清理本次成功夹具。`git diff --check` 退出 0。未运行 HTTP、原生 App、真机或真实年龄核验；此卡仍不含完整 HTTP finish 合同。

被测文件 SHA-256：

- `repository/finish.go`：`c7bcc6915c9b986a251832ea20a78c63e7de8a8ad270b4a54fca47289aeed2cc`
- `application/finish.go`：`b49340f63ff59330988d3dcc3419504b8ea425976ec51b8815e5b253b8d87342`
- `application/finish_test.go`：`cfeade854841f234697f6a91a9f0979d8d125ad2fec009ce8071195140d03356`
- `mysql_smoke.py`：`53af71b92ad066727ddd23f5c5b63a02a1a029d41a00269ae8f8051d9758bde3`

集成提交 `b5ca21d` 上使用 `python3 -B docs/evidence/A04/A04-04/mysql_smoke.py`，在同一私有数据库中同时执行本完成测试与认证记录读写测试，均通过；任务图检查有效。本次复测不代表完整 HTTP 合同或最终接受已完成。

该内部事务尚未支持实际日期修改、可选总时长/RPE/备注、计划训练统计及 HTTP 幂等响应；真实年龄核验、设备批准、客户端接线和双设备同步也未完成。禁止用本测试冒称完整 App 或正式保存接口已交付。
