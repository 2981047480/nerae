# A01-04 epoch 初始化边界修复

## 状态与范围

本轮 A01-04 修复已在隔离 MySQL race harness 通过，未提交。基线/当前 HEAD：`f30f90d88960d883d45d8ad682f393737a4b649f`。只修改 challenge store、对应测试和本证据目录下的 harness/report；迁移及其他模块未修改。

## 变更

- `checkEmailEpoch` 现在只校验 `EmailIdentityProtector.KeyEpoch()` 与 `auth_email_identity_key_epoch` 的现有行；缺行、查询错误或标签不匹配统一返回 `ErrChallengeStore`，不再懒插入。
- `IssueInput` 注释说明 ChallengeID/nonce 必须用 CSPRNG 生成；验证码允许使用已审核的服务端派生机制。
- 挑战集成测试先验证空库未初始化时 `Issue` 和 `Attempt` 均失败且 epoch 未创建，再显式通过 `bootstrap.IdentityEpochInitializer.Initialize` 初始化并提交，之后原有挑战行为测试照常运行。
- 在已有挑战数据时删除 epoch，确认 `Issue`、`Attempt` 和 bootstrapper 都拒绝且不重建；测试夹具随后直接恢复已知 key tag，以便完成本测试剩余场景。
- 私有 harness 迁移顺序增加 `user/0001_user.up.sql` 和 `auth/0003_email_identity.up.sql`，以满足真实 bootstrap schema 验证。

## 验证

- `python3 -B docs/evidence/A01/A01-04/mysql_smoke.py`：退出码 `0`。在新建、私有、Unix socket only MySQL 上依次执行 auth/0001、auth/0002、user/0001、auth/0003 迁移与 `go test -race -count=1 -v ./internal/modules/auth/challenge`；成功后关闭并清理本脚本创建的临时目录。
- 离线编译命令 `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test ./internal/modules/auth/challenge`：退出码 `0`。此命令未配置测试 DSN，数据库用例会跳过；真实数据库证据来自上述 harness。
- 未运行其他 auth 模块测试。可能受此变化影响的夹具包含 `identity/email_test.go`、`session/store_test.go`、`session/age_test.go`、`application/email_login_test.go`、`approval/exchange_test.go`、`approval/store_test.go`；它们若直接调用 `challenge.Store.Issue`，需由各自 owner 升级夹具，在测试前显式初始化 epoch。本任务未越域修改这些文件。

## SHA-256

```text
9a051685b820193f30209cdd7cf17554b216338f6622c53bb15321c9126c26e8  apps/api/internal/modules/auth/challenge/email.go
b583ba1c0e794a32533f952cc35da55f2e50492a7bdd47d609640e0401419ede  apps/api/internal/modules/auth/challenge/email_test.go
89c8ddfe814cf0af1fac2477cb7f36651419806b428f97bde67657738f196b65  docs/evidence/A01/A01-04/mysql_smoke.py
a02b69a38e87b8bec9ff10f1bee380451fe4d359ca00b8863a5835c4f9f756ec  apps/api/migrations/user/0001_user.up.sql (只读)
373e34b4915f595b18c8c5dc911ecabc32a6e45f350027891c372d188b50f38e  apps/api/migrations/auth/0003_email_identity.up.sql (只读)
```

真实邮件服务、HTTP 登录路由、跨挑战限流、生产数据库、真实设备与跨端登录均不在本次验证范围；本结果不等于完整认证链路验收。

## Lead 集成复验

同步调整 13 个现有 auth、幂等和训练测试夹具，在签发合成挑战前使用同一受控 bootstrap；未通过修改生产代码或测试替身绕过新检查。集成工作树分别运行隔离 MySQL 的 A01-04、A01-05、A01-09、A01-11、A01-13、A04-08、A04-09 与 F02-04 harness，均退出 0；其中 A01-13 包括队列到本机 TLS SMTP 的实际接缝，A04-09 包括真实客户端适配器到本机 TLS HTTP 的两种记录写入。`go test -run '^$' ./...` 只检查所有包编译，不视为运行行为测试。以上均使用合成身份、数据和本机隔离实例，不表示真实邮箱、两个物理设备或正式 App 已通过。
