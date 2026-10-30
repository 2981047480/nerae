# A04-04 · 回归测试范围修复

2026-10-30，邮箱 identity epoch 改为显式初始化后，旧版本卡 `mysql_smoke.py` 运行整个 `workout/application` 包，在同一私有数据库先后运行使用不同合成密钥的 A04-04 与 A04-08 夹具，导致 A04-08 的 epoch 检查失败，退出 1。失败目录 `/private/tmp/nerae-workout-integration-mysql-c2u_cqnm` 保留；该结果是测试隔离冲突，不是训练业务通过的证据。

本卡 harness 改为只运行自身 `TestAuthenticatedWorkoutTransactions`，使用原有私有数据库和该测试需要的迁移；重跑退出 0。A04-08 幂等事务在其自己的隔离实例独立通过；完成、列表和 HTTP 由各自任务的 harness 验证。缩小本卡测试范围不代表其他任务的测试自动通过。
