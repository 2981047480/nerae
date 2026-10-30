# A04-02 · 自由训练记录持久创建进度

状态：partial，未接受。Lead 基于 `b3a11e0` 实现自由训练记录的 MySQL 创建与按主体事件键恢复；创建冻结实际日期及有效时区，省略时按合同使用 `Asia/Shanghai`，明确空字符串拒绝。原始记录从不虚构开始时间、记录确认时间或训练时长。此存储原语不鉴权、不开放 HTTP；调用方必须以真实服务端认证结果确定主体并检查年龄准入。

`python3 -B docs/evidence/A04/A04-02/mysql_smoke.py` 退出 0：使用本机临时 MySQL 8.4 私有 Unix socket、独立测试数据库与合成数据，成功后关闭并删除本次专属目录。Go race 测试覆盖同事件重放、同键异请求冲突、同键跨主体隔离、大小写区分、事务回滚、禁用/不存在主体、时区跨日与未来日期、默认时区回读及与显式默认值归一、日志脱敏。并发测试先持有用户行锁，并通过 `performance_schema.data_lock_waits` 确认两个事务实际等待，再释放后验证相同请求只产生同一记录、不同请求只有一笔成功。

最初使用用户共享锁时，隔离测试两次重现首次插入的 InnoDB 间隙锁死锁；改为先独占用户行、再操作训练会话，复测通过。后续所有同时操作 user 和 workout 的事务必须遵守同一锁顺序；同一用户并发创建会串行，其他用户互不阻塞。单独复核指出时区默认值未贯通，现已在存储入口区分省略与明确空值，按归一结果计算创建摘要并再次通过测试。HTTP 接入仍须拒绝显式 JSON null、校验其他请求字段并遵守同一合同。

事件键实际存为 `VARBINARY(256)`，保证区分大小写与结尾字节并容纳最多 64 个四字节 UTF-8 字符；服务端按字符数限制 8–64，数据库额外限制字节数 8–256。集成时已将 ER 的物理类型说明同步为此比较语义。当前迁移仅允许自由记录；计划引用、实际组、加密内容、统计、版本更新及完整幂等响应均未实现。

被测文件 SHA-256：

- `repository/session.go`：`b2c5117484d51cd3c8f215090c37f9209e5a59f7a15ca65af2ac925f78d1cb65`
- `repository/session_test.go`：`d5990080de4f9fbcd4329b0617323db50aae84c0a71e20845cecd7636e74e61d`
- `workout/0001_workout_session.up.sql`：`d52c48160d66f190b885898def7ad6c523e8af180063461d8a53fdb14cb17e82`
- `mysql_smoke.py`：`c139d28493a62ec6489b9385664f95360fb96e185553761bc62d35993e93e2f3`

集成提交 `8e85b88` 的上述同一文件版本再次通过隔离 MySQL 测试；补齐迁移顺序后，全局清单检查报告 5 项有效，`python3 -B scripts/plan.py check` 报告有效。此复测不代表已满足最终接受条件。

独立认证、年龄门槛、会话授权、实际组读写、HTTP 幂等、客户端接线、真实跨设备同步和 App 安装均尚未完成，不能把本次数据库测试当作用户可用的训练记录。

## 共享事务适配增量

基线 `5fe0862`：`CreateFree` 保留原 `*sql.Tx` 接口，并调用新增 `CreateFreeTx`；后者只使用调用方提供的事务能力，仍要求上层完成身份、年龄和资源授权。固定参数化语句从实际仓储代码导出为防篡改副本，供后续幂等写入的 SQL 白名单引用。

`go test -race -count=1 ./internal/modules/workout/repository` 退出 0，未设置 DSN，数据库测试跳过。原有 `mysql_smoke.py` 首次退出 1：历史脚本准备了会话表却运行整个 repository 包，新加入的组/顺序测试缺少表；`TestFreeSessionTransactions` 当次通过。已将脚本限定为本卡覆盖的创建、输入和脱敏测试，重跑 `python3 -B docs/evidence/A04/A04-02/mysql_smoke.py` 退出 0；隔离 MySQL 验证 nil 事务失败、空 SQL 策略拒绝、固定 SQL 策略允许创建且回滚、原有重放与并发行为。测试策略探针并非真实幂等执行器，后续仍需接线测试。失败目录 `/private/tmp/nerae-workout-session-mysql-bs3mhzgm` 已保留。

- `session.go`：`6ae46c16f2c2d7732f013eb1c25da6b271812e6a0d1f09556c677d1016923152`
- `session_test.go`：`19dbcb4174688407529bb9088bc2059c0dc6bf4f535aa6d5c23ae662b3e45bf2`
- `mysql_smoke.py`：`34ad8c2b9bba6b38518a766ef6387784712659ec71b0c12345f8e76ad0bf31db`
