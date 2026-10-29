# A01-09 · 已批准新设备兑换与会话事务进度

状态：partial，未接受。Lead 基于 `f4668d6` 实现内部兑换事务：新设备必须同时持有申请轮询秘密和绑定审批 ID、nonce、用途与 JTI 的设备私钥签名；服务端检查原身份、主体版本、批准会话及原设备仍有效，在同一事务消费证明、登记目标设备、创建同主体会话并消费审批。事务由兑换器自行提交，成功提交前不返回令牌；错误路径回滚。

`python3 -B docs/evidence/A01/A01-09/mysql_smoke.py` 的历史运行退出 0。测试仅在本机新建私有 Unix socket MySQL 8.4 中应用 9 份迁移，使用合成邮箱与设备密钥；成功后关闭并移除专属目录，不发送真实邮件。覆盖未批准/已拒绝、错误秘密/私钥/nonce、原身份/账号/设备/会话撤销或过期、主体版本变化、目标密钥已撤销或被另一主体占用、同设备 JTI 跨申请重放、已消费申请重复兑换、事务中途失败回滚与同证明重试、同申请并发唯一成功。测试还实际锁住目标设备行，在申请与原会话仍有效时单独让签名证明过期，验证兑换回滚且重试不受错误防重标记影响。

两把不同的合成设备私钥分别取得同主体会话：原会话通过 `DeclareAdult(..., true)` 记录明确的 18+ 自我声明，再创建并写入训练组；新会话读回并修改，原会话再读到更新。随后本人通过 `ClearAdultDeclaration` 撤回声明，两设备会话均因年龄门槛被拒绝读取；本人重新明确声明后，新设备恢复读取原有记录。此声明是自我声明，不是身份或实际年龄核验；撤回并发防护以及 HTTP/UI 撤回入口尚未开放。此验证使用私有数据库和合成设备密钥，不是实际手机、网络 HTTP 或 SMTP 验收。只读安全复核未发现可直接复现的越权或重复签发；原会话单独过期、跨申请 JTI 重放以及证明单独过期的负例均已补充并重新通过。

被测文件 SHA-256：

- `approval/exchange.go`：`4f47e41b531242248eaa90f34b8faa3335217f572a1eb2f1764d387eb39f8f35`
- `approval/exchange_test.go`：`bb6ab977b7e5567ce4358fb2ca55351ced528b9107e263036ef8f128b5c72120`
- `auth/0006_approval_exchange.up.sql`：`c38b3f5683124afa03f7ee206df9baac26dd428922e4cf3ffd1b9b3746dfbc5f`
- `mysql_smoke.py`：`5e38f810a5c6b0d481cea659609022b67e4e99eeb99bcc6540f978a97391fc6b`

集成提交 `8a2bf8f` 上，申请/确认及兑换测试在同一个私有数据库共同通过，包含两把合成设备密钥对同一训练记录的读写；9 项迁移清单和任务图检查有效。此复测不代表真实手机、HTTP 或最终接受已完成。

当前兑换器自行提交；提交结果不明或响应丢失后无法安全重放，必须先设计与交易同提交的加密幂等响应和撤销复查，再开放 HTTP。真实发信、限流、原生设备安全存储、微信跨身份批准、恢复码、动作原始顺序、客户端接线和真实双设备验收均未完成。

## 两设备动作顺序回读补测

基线：`24edce24f6ca7bcd4eefb1f250ae4559af0e3902`。测试仅扩展 `approval/exchange_test.go`：原设备按 B、A 顺序保存两个不同动作条目；批准设备读回 B、A 后修改 B 并重排为 A、B；原设备再读回并核对新顺序、修改后的重量及版本从 1→2→3。每次 `SaveSets` 都提供完整动作条目顺序。

隔离迁移清单追加 `workout/0003_workout_exercise_order.up.sql`。`python3 -B docs/evidence/A01/A01-09/mysql_smoke.py` 首次在沙箱中 exit 1：MySQL 8.4.13 初始化期间 `SIGSEGV (Invalid permissions for mapped object)`，尚未执行测试；脚本保留失败目录 `/private/tmp/nerae-approval-mysql-yedw8uve`。使用正常授权对同一命令重试，exit 0：两个 approval 套件测试通过，成功运行创建的私有 Unix socket 数据目录已关闭并移除。`git diff --check` exit 0。

本补测仅使用隔离 MySQL 与合成 P-256 设备密钥，不代表真实手机、HTTP、SMTP、真实账户登录或正式年龄核验验收。变更后 SHA-256：

- `approval/exchange_test.go`：`982dc1a7772e210a8d478cede756e09c1a86294a88631bfb2d387f8bdf034898`
- `mysql_smoke.py`：`f58f6dd2de7e2ee13ec575af98e5c5b4e1e71a5227709d6a3909d6c9b6e622b5`

本次补测再次运行 `python3 -B docs/evidence/A01/A01-09/mysql_smoke.py`，退出码 0；`TestApprovedDeviceExchange` 与 `TestNewDeviceApprovalTransactions` 均通过。沙箱内首次尝试只失败于私有 MySQL 初始化（exit 1，未进入测试）；在受限授权下同一隔离 harness 成功，私有 socket 数据库已关闭并清理。最终测试文件 SHA-256：`a06294acc2d47f8abc491d58fd689a79509eaa4c9cfb3ece32699e2213abf2ae`。
