# A04-04 · 会话与训练记录事务接缝进度

状态：partial，未接受。Lead 基于 `7f29780` 将用户会话检查、年龄状态、自由训练记录创建、实际组读写组合在一个数据库事务里；提交前再次检查当前主体与授权，任何错误回滚。记录读取验证所属主体、日期时区、动作密文和密钥 epoch，仅返回未删除的实际组；内部快照不可直接用于 JSON 或日志。

`python3 -B docs/evidence/A04/A04-04/mysql_smoke.py` 退出 0：本地新建私有 Unix socket MySQL 8.4，按全局顺序应用含动作顺序表的 8 份迁移，使用合成邮箱和测试生成的设备密钥，成功后关闭并删除本次专属目录。Go race 测试建立两个同主体的不同会话；另一个会话可读取、修改同一份持久化记录，旧版本不能覆盖新版本。不同主体不可读取或修改；未声明成人、设备撤销及令牌篡改被拒绝。测试现在通过 `auth.DeclareAdult(..., true)` 建立隔离账号的明确 18+ 自我声明；在有序训练记录写入后，本人调用 `ClearAdultDeclaration`，原有效会话的读取与写入均返回 `ErrAgeRequired`，直接核对会话版本/状态、组数/重量和顺序行均未变化；本人通过既有会话重新声明后，原有记录读取恢复。该声明仍只是自我声明，不证明实际年龄。写入后在同一事务内移除年龄标记或撤销设备，最终复查失败且训练写入完整回滚。异常存储日期或时区也失败关闭。

只读复核未发现接缝中的跨主体、年龄声明绕过或提交前撤权漏洞。组集合仍以 map 保存，不能以 map 遍历推断顺序；动作顺序由单独顺序表和有序快照表达。HTTP 响应、客户端接线和跨端恢复仍未完成。测试只覆盖同设备不同会话，并未验证不同设备登录或同步。

被测文件 SHA-256：

- `repository/read.go`：`f3f3bbfd6d1803a3c527222f957da35a07340b6f5d1b2e141f96e3899fc3ed12`
- `application/workout.go`：`c02ff1df06bf2bed4d21a1c1e971f8316213af842287dfea70ab9dc7b9bac890`
- `application/workout_test.go`：`6f5a1a448e250cb3f96e3b266b9133f37c7bdf9fc3b7bb879943e55ea43d86e0`
- `mysql_smoke.py`：`595863f95aef1e20df9e478e011713ba1cc35c28d7dc2045e2b2764f77deaa6d`

集成提交 `af7ded4` 的原始文件版本通过隔离 MySQL 接缝测试。随后将年龄拒绝后的记录计数限定到测试主体，集成提交 `b5ca21d` 上当前文件与训练完成测试在同一隔离数据库共同通过；任务图和 8 项迁移清单检查有效。该复测不表示 HTTP、双设备同步或最终接受已完成。

真实年龄核验、发送邮箱验证码、设备批准与恢复、HTTP 和幂等响应、动作排序、客户端接线、双设备测试及 App 安装均未完成。

## 本轮事件 ID 读回接缝

`repository.ReadFree` 在既有 `validateFreeInput` 校验 `client_event_id` 后，将原始值放入内部 `Snapshot.ClientEventID`。快照现有 `String`、`GoString`、`MarshalJSON` 与 `LogValue` 脱敏仍覆盖该字段；没有增加 HTTP 响应、日志或路由接线。真实隔离应用服务测试证明：同一主体的两个不同已认证会话读回同一个已存事件 ID；另一个主体无法读取记录；满足列长度约束的非法 UTF-8 持久值被拒绝为 `repository.ErrUnavailable`；另一条同主体记录读回其自身、不同的事件 ID。测试在输出检查中也断言事件 ID 不会出现在格式化、JSON 或日志结果内。

本轮验证命令与结果：

- `GOTOOLCHAIN=local GOPROXY=off GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race ./internal/modules/workout/repository ./internal/modules/workout/application`：退出 0。
- `python3 -B docs/evidence/A04/A04-04/mysql_smoke.py`：审批路径下退出 0。脚本在新建的私有 Unix socket MySQL 8.4.13 datadir 应用 8 项迁移并运行两个应用 race 测试；测试通过后关闭数据库并删除本次目录。
- `git diff --check`：退出 0。

验证过程中的限制也保留记录：普通沙箱内首次 MySQL 初始化以 SIGSEGV 失败，失败目录 `/private/tmp/nerae-workout-integration-mysql-kwu4j1h3` 按 harness 约定保留；审批运行第一次到达应用测试时，1 字节损坏夹具被数据库现有 8 字节最小长度约束拒绝，之后改为 8 字节非法 UTF-8 并在最终完整隔离运行中通过。另一次失败测试目录 `/private/tmp/nerae-workout-integration-mysql-j45bxhjc` 同样保留。未删除这些失败现场或此前临时目录。

此接缝仅让后续内部授权 presenter 有经过校验的源 ID 可用；公开读响应、事件幂等回执、双设备恢复流程及最终接受仍未验证。
