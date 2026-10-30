# A04-04 · 本人训练详情事务适配

状态：partial。基线 `f7b8a04` 的 `ReadFree` 保留 `*sql.Tx` 入口；新增 `ReadFreeTx` 只读取调用方提供的事务能力，固定 SQL 清单供后续幂等策略组合使用。读取仍是持久层原语，调用方必须在同一事务落实身份、年龄和资源授权。

`python3 -B docs/evidence/A04/A04-04/mysql_smoke.py` 首次退出 1：测试将字符串形式的令牌主体直接传给 uint64 仓储接口，Go 编译失败，隔离目录 `/private/tmp/nerae-workout-integration-mysql-2h2u4i9v` 保留。修正为解析服务端签发的主体 ID 后重跑退出 0：覆盖 nil 事务、有效主体恢复原始记录、其他主体隔离，以及原有年龄、会话和训练事务回归。测试使用真实隔离 MySQL、合成身份和设备；未接入真正的幂等执行器或端侧登录。

- `repository/read.go`：`798d901e1cd4c04afd1c3e07146c66079f0b5d465dfd3e68f645200819939e97`
- `application/workout_test.go`：`98e9cdafe63079886d8a4028092fdf65c71bd95eb4ca5922ec019c5d67adae6b`
