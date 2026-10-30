# A01-10 · 18+ 声明版本化事务基础

状态：实现及本地私有 MySQL/race 验证完成，尚未接受。此代码是内部事务基础，不表示 OpenAPI、HTTP、客户端或训练页面已接线。18+ 仍是用户自我声明，不是身份证明或精确年龄核验。

新增 `user.age_declaration_version`，默认从 1 开始，由 user 模块迁移添加，范围受数据库 CHECK 限制在 JavaScript 安全整数 `1..2^53-1`。新内部入口 `DeclareAdultAtVersion` 和 `ClearAdultDeclarationAtVersion` 从当前 bearer 会话取得主体，在认证持有的用户行锁内比较 `expectedVersion`；每个接受的明确动作都递增版本，包括对已声明状态再次明确声明和撤回已经为空的状态。过期版本返回 `ErrAgeDeclarationConflict`，不改变状态；无效/不安全整数、版本耗尽、存储不可用及无效会话分别返回固定分类。拒绝声明不写入。撤回不注销会话；成年门槛仍须由实际业务事务重新校验。

原 `DeclareAdult` / `ClearAdultDeclaration` 仅为兼容旧内部测试保留，缺少版本门槛，仍可被旧请求重放后改变状态；不得用于生产处理器或对外入口。应在未来 HTTP/应用接线时只调用版本化接口，并在废弃旧接口后删除它们。当前合同没有版本读取、期望版本、撤回操作、幂等键或路由，本任务未修改 OpenAPI、迁移注册、客户端或路由。

纯 Go 检查：版本范围 race 用例和 session 包编译通过。测试前检查 `/private/tmp` 可用空间为 2,425,844 KiB，高于 harness 的 350 MiB 门槛；Lead 确认 A01-23 harness 已结束后独占运行本测试。首次受限沙箱运行 `python3 -B docs/evidence/A01/A01-10/mysql_smoke.py` 退出 1，`mysqld --initialize-insecure` 收到 SIGSEGV (signal 11)，失败目录保留在 `/private/tmp/nerae-auth-session-mysql-0a_t_yoe`。按任务要求授权模式重跑相同命令，退出 0；`TestAdultSelfDeclarationTransactions` 通过，harness 使用私有 Unix socket MySQL，加载新增 `user/0002_age_declaration_version.up.sql`，成功目录清理。覆盖声明/撤回两种竞争锁顺序、撤回后迟到及重复旧版本、空状态撤回推进、跨主体、无效 bearer 与取消请求分类、回滚、版本耗尽和数据库 CHECK。`git diff --check` 通过。

不应把这些内部测试或版本化原语称为用户已经确认、HTTP 已可用、训练记录已接通或正式年龄核验。A01-10 原报告保留为历史记录。
