# A01-15 邮箱身份 epoch 启动初始化基础

## 交付

新增 `apps/api/internal/modules/auth/bootstrap.IdentityEpochInitializer`。调用方须在服务接入认证流量前，以新建 SQL 事务调用 `Initialize`，且仅在成功后提交。初始化器不会自行提交或记录密钥：已有 epoch 行以 `FOR UPDATE` 锁定并用 `hmac.Equal` 核对 `EmailIdentityProtector.KeyEpoch()` 标签；匹配后仍会以 `LIMIT 0` 安全探测 slot、challenge、login_identity 的完整必要列。缺失 epoch 时，使用锁定当前读检查 slot、challenge 和所有 `kind='email'` 的 `login_identity`（不区分 active/revoked），只有三表皆空才插入标签。查询/迁移/密钥/存量任一不符均返回统一 `ErrIdentityEpoch`，事务归调用方回滚。零行 schema 探测不读取或拒绝正常已有身份数据。

## 验证

- `GOTOOLCHAIN=local GOPROXY=off GOCACHE=/private/tmp/nerae-go-api-cache GOMODCACHE=/private/tmp/nerae-go-api-mod /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race -count=1 ./internal/modules/auth/bootstrap`：退出 0，包编译通过；未设置隔离 MySQL DSN 时数据库用例按设计跳过。
- `python3 -B docs/evidence/A01/A01-15/mysql_smoke.py`：普通沙箱中退出 1，MySQL 8.4.13 初始化发生 `SIGSEGV (Invalid permissions for mapped object)`，尚未开始测试；失败目录保留为 `/private/tmp/nerae-email-epoch-mysql-d8wcrdru`。
- 同一命令经正常授权以本机隔离执行：退出 0。脚本创建新私有 datadir、禁用 TCP，只通过本次 Unix socket 运行 MySQL；仅应用 user 0001、auth 0002/0003 迁移。Go race 实测通过：空库首次初始化、同 key 重启、错 key 拒绝、已有 active email identity 不误拒、匹配 epoch 时三张 email 表任一缺失及必要列缺失均失败关闭、epoch 缺失时存量 slot/challenge/已撤销 email identity 拒绝、调用方回滚不留 epoch、已有旧快照时当前读拒绝、不同 key 并发只能留下其中一个 epoch、缺迁移表失败关闭及日志/格式化/序列化脱敏。成功后只关闭并删除本次新建的临时实例目录。
- 在 `apps/api` 中执行 `/private/tmp/nerae-go-toolchain-1.27.3/go/bin/gofmt -d internal/modules/auth/bootstrap/identity_epoch.go internal/modules/auth/bootstrap/identity_epoch_test.go`：无输出；对 harness 执行 Python `ast.parse`：退出 0。

## 部署边界与未完成项

当前 `challenge.Store.checkEmailEpoch` 仍保留缺行时的懒初始化写入（`apps/api/internal/modules/auth/challenge/email.go`）。本卡未修改它，也不能保证本初始化器与旧版/懒初始化代码并发时的行为。部署时必须先停止所有旧认证写入进程、串行部署并确认初始化器在服务接流量前成功；后续需独立移除懒初始化并在同版集成提交重测。此实现不是服务启动装配或线上切流验收。

## 文件 SHA-256

- `identity_epoch.go`: `eacc390679af8ebf334f04b9926833138b34eaa8d68ed4f2a49d6d64f2cf4cc7`
- `identity_epoch_test.go`: `22465306e5f5948c911a1bb50a07d300f5beb5d6680248fa88a0584f645abd76`
- `mysql_smoke.py`: `a3703fad9d8d8206f3f11ba269773969cf2a7e134b8ebba9783f7b4bb289a519`
