# A01-10 · 18+ 声明版本化事务基础

状态：实现及本地私有 MySQL/race 验证完成，尚未接受。此代码是内部事务基础，不表示 OpenAPI、HTTP、客户端或训练页面已接线。18+ 仍是用户自我声明，不是身份证明或精确年龄核验。

新增 `user.age_declaration_version`，默认从 1 开始，由 user 模块迁移添加，范围受数据库 CHECK 限制在 JavaScript 安全整数 `1..2^53-1`。新内部入口 `DeclareAdultAtVersion` 和 `ClearAdultDeclarationAtVersion` 从当前 bearer 会话取得主体，在认证持有的用户行锁内比较 `expectedVersion`；每个接受的明确动作都递增版本，包括对已声明状态再次明确声明和撤回已经为空的状态。过期版本返回 `ErrAgeDeclarationConflict`，不改变状态；无效/不安全整数、版本耗尽、存储不可用及无效会话分别返回固定分类。拒绝声明不写入。撤回不注销会话；成年门槛仍须由实际业务事务重新校验。

原 `DeclareAdult` / `ClearAdultDeclaration` 在初始实现时曾作为兼容方法保留；2026-10-31 跟进已将两个无版本公开入口及其无版本事务实现删除。当前唯一公开的声明/撤回写入入口要求 `expectedVersion`。本任务仍未修改 OpenAPI、迁移注册、客户端或路由。

纯 Go 检查：版本范围 race 用例和 session 包编译通过。测试前检查 `/private/tmp` 可用空间为 2,425,844 KiB，高于 harness 的 350 MiB 门槛；Lead 确认 A01-23 harness 已结束后独占运行本测试。首次受限沙箱运行 `python3 -B docs/evidence/A01/A01-10/mysql_smoke.py` 退出 1，`mysqld --initialize-insecure` 收到 SIGSEGV (signal 11)，失败目录保留在 `/private/tmp/nerae-auth-session-mysql-0a_t_yoe`。按任务要求授权模式重跑相同命令，退出 0；`TestAdultSelfDeclarationTransactions` 通过，harness 使用私有 Unix socket MySQL，加载新增 `user/0002_age_declaration_version.up.sql`，成功目录清理。覆盖声明/撤回两种竞争锁顺序、撤回后迟到及重复旧版本、空状态撤回推进、跨主体、无效 bearer 与取消请求分类、回滚、版本耗尽和数据库 CHECK。`git diff --check` 通过。

不应把这些内部测试或版本化原语称为用户已经确认、HTTP 已可用、训练记录已接通或正式年龄核验。A01-10 原报告保留为历史记录。

## Lead 集成与迁移登记

后续集成删除无版本入口，并在认证会话的 `ProfileForResponse` 中于用户行锁下读取声明版本；锁等待后读取更新、版本边界和会话撤销在 A01-07 隔离 MySQL/race harness 实跑通过。Lead 将 `user/0002_age_declaration_version.up.sql` 追加为全局迁移清单第 15 项，未重排既有项目；`go run ../../scripts/dev/migrate.go check` 退出 0，确认 15 项哈希。新增迁移后，A01-23 与 A01-24 的私有 MySQL/Redis/本机 TLS 联测重新运行，均退出 0。

版本目前仍未进入 OpenAPI 或 HTTP 响应，声明/撤回也没有生产路由；遇到提交结果未知必须先读取当前状态并由本人决定，不能自动重新声明。该内部实现不构成身份或年龄核验。

## 2026-10-31 · CAS 入口清理跟进

跟进提交删除了公开的无版本 `DeclareAdult` / `ClearAdultDeclaration`，以及仅供这些旧入口调用的无版本事务实现。现在 session 包唯一公开的声明/撤回写入入口都要求 `expectedVersion`。测试使用前一次版本化结果或在认证 bearer 后读取的当前版本；没有生产测试后门。

受影响的 workout application、workout transport 和 approval 测试已改用版本化入口。它们的私有 MySQL harness 均在原有迁移序列中 `user/0001_user.up.sql` 后加入既有 `user/0002_age_declaration_version.up.sql`。A01-09 另补入该测试包现有回执恢复用例需要的 `auth/0009_email_login_receipt.up.sql`；其余迁移相对顺序保留。

本轮验证：
- `/private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race ./internal/modules/auth/session ./internal/modules/workout/application ./internal/modules/workout/transport ./internal/modules/auth/approval`（离线缓存配置）— exit 0；不带隔离 DSN 时 DB 测试按设计跳过。
- `python3 -B docs/evidence/A01/A01-10/mysql_smoke.py` — 最终授权运行 exit 0，A01-10 CAS/race 用例通过。第一次普通沙箱运行在 MySQL 初始化时 SIGSEGV，保留 `/private/tmp/nerae-auth-session-mysql-ognbxrx7`；两次授权运行中的测试断言失败分别保留 `/private/tmp/nerae-auth-session-mysql-z9c283m8` 与 `/private/tmp/nerae-auth-session-mysql-9kzbtz4b`，已修正断言分类/版本比较后通过。
- `python3 -B docs/evidence/A04/A04-04/mysql_smoke.py`、`A04/A04-06/mysql_smoke.py`、`A04/A04-07/mysql_smoke.py`、`A04/A04-08/mysql_smoke.py`、`A04/A04-09/mysql_smoke.py` — 均 exit 0；相应私有 MySQL/race 测试通过。
- `python3 -B docs/evidence/A01/A01-09/mysql_smoke.py` — 首次 exit 1，既有 `TestApprovalReceiptRecovery` 需要但夹具未载入 `auth/0009_email_login_receipt.up.sql`，失败目录保留 `/private/tmp/nerae-approval-mysql-zpu83wlp`；加入该迁移后重跑 exit 0，approval 包三个数据库测试通过。
- `python3 scripts/plan.py check` — exit 0；`git diff --check` — exit 0。
