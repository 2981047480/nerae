# A01-21 实施证据

## 状态

**partial：** 已交付事务绑定的密文回执仓储、专用迁移和私有 MySQL race 集成测试。此包只持久化/恢复密文；没有实现真实登录编排、设备签名或当前授权复验、凭据发行、撤销检查、提交后释放，也没有 commit-ack 故障注入。需 Lead 独立审查安全语义和接线边界后决定是否接受。

## 实现

- `apps/api/internal/modules/auth/loginledger/store.go`：要求显式事务；校验身份与回执 epoch；按身份 epoch、回执 epoch、邮箱 slot、challenge、receipt 顺序锁定；未过期行保持摘要/挑战冲突语义；过期行按数据库 UTC 判定，仅当 slot 仍指向请求 challenge 时在当前事务删除旧行并返回新句柄，回滚可恢复旧行；slot 换绑后的过期行失败关闭。Lock 仅解析密文 framing 并校验 AAD 元数据，不在签名和当前授权复验前做 GCM Open；Put 仅在原事务和首次句柄内认证服务端新密文的 AAD 并插入。调用方必须独立完成签名、验证码、JTI 与当前授权验证后才可创建新回执，仓储不返回解密材料。
- `apps/api/migrations/auth/0009_email_login_receipt.up.sql`：单行 epoch、按受保护 scope 与幂等 HMAC 唯一的收据、challenge 外键、有限类型/长度/到期约束及查询索引。未连接共享迁移注册。
- `store_test.go` 与 `mysql_smoke.py`：受限 DSN 检查和一次性私有 socket-only MySQL。覆盖显式初始化、错误/丢失 epoch、首次写入及恢复、同 key 请求冲突、slot/challenge mismatch 与数据库故障的错误分类、句柄重复写入、Put 时同事务 slot 改绑拒绝、同 key 并发序列化、跨 scope、普通回滚、过期后同幂等键的新请求删除旧行并成功写新回执、删除后事务回滚恢复旧行、未过期异请求冲突、过期且 slot 换绑时拒绝删除/创建、邮箱 slot 换绑后旧精确回执仍可取回、Lock 延迟解密并由后续 Open 拒绝篡改密文，以及密文不含邮箱/OTP/结果明文。

## 验证

运行：

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race -count=1 ./internal/modules/auth/loginledger
python3 docs/evidence/A01/A01-21/mysql_smoke.py
git diff --check
```

私有 harness 在 Unix socket、`--skip-networking` 的一次性 MySQL 上完成迁移与 Go race 集成测试，退出码 0；成功后销毁数据库目录。本隔离树的 `scripts/dev/migrations.json` 实际有 13 项，脚本逐项验证 SHA-256 并按顺序载入，再附加尚未注册的 A01-21 `auth/0009_email_login_receipt.up.sql`，本次实际加载 14 个 migration 文件。当前隔离树未包含 Lead 另一分支新增的 manifest 第 14 项，因此该项未在本次验证。测试覆盖过期后相同幂等键、不同请求摘要重新写入；删除后回滚可恢复旧行；未过期异请求冲突；slot 换绑的过期行保留且旧 challenge 不可新建。独立 race 包测试退出码 0。`gofmt -d` 无输出，Python harness compile 检查通过，已对新增文件逐一执行 `git diff --no-index --check`（无 whitespace 输出）；`git diff --check` 退出码 0。

## 限制

目前 `Initialize` 的“表空”检查和 epoch 写入保护依赖 MySQL/InnoDB locking read 语义；Lead 应审查部署目标 MySQL 版本与事务隔离级别。调用方仍负责将 `scope` 从服务端受保护邮箱派生、在同一事务里完成认证和授权复验，并只在提交确认后释放结果。此证据不表示已完成密钥轮换或任何生产接线。
