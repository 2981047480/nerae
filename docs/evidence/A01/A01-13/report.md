# A01-13 邮件 outbox 接缝证据

## 状态

`partial`：隔离 outbox、租约状态转换和 SMTP 本地 TLS sink 检查通过；首次挑战所需的 identity key epoch 缺少显式安全 bootstrap，尚不能在空库中按当前调用顺序启动完整签发流程。未提交，基线 `ad70c2905278bff307821e3b6f3a41b18ca0353f`。

## 实现与验证

- 新增 issuance outbox：队列仅持久化 challenge ID、码长、状态与租约。入队校验当前 slot、challenge、邮箱密文/key ID 和派生码摘要；发送前再次校验。epoch→slot→challenge→outbox 顺序用于事务锁定。
- `InitializeCodeKey` 先以 `FOR UPDATE` 锁定/检查 epoch；已有时仅常量时间校验 tag 和 key ID。仅 epoch 缺行时，再以 `FOR UPDATE` 当前读确认 outbox 无历史后插入。旧一致性快照不能隐藏已提交的队列行。同 key ID/tag 可重启；不同密钥或 key ID 拒绝；epoch 缺失但 outbox 有历史时不补建。并发初始化可能有一方死锁/失败；调用方必须回滚并在新事务重试，失败不会伪装成功。
- claimed 租约可回收；sending 租约过期变为 `unknown`，不自动重发。worker 恢复后若租约已过期，会跳过 SMTP；发送 context deadline 绑定持久租约。完成调用返回实际落库状态，过期 accepted 返回 `unknown`。
- 本地 Unix socket MySQL race harness：`python3 -B docs/evidence/A01/A01-13/mysql_smoke.py` 连续 3 次退出码均为 `0`。实际跑过 `go test -race -count=1 -v ./internal/modules/auth/issuance ./internal/modules/auth/mail`；outbox 并发/回滚/过期/迟到结果、epoch 首次并发初始化/重启/异钥/旧快照和 mail 测试中的 loopback TLS 成功、明确拒绝、未知结果、证书、取消等用例均通过。没有向外发信。
- `go vet ./internal/modules/auth/issuance ./internal/modules/auth/mail`，退出码 `0`；离线 `go test ./internal/modules/auth/issuance`，退出码 `0`；`gofmt -d` 无差异；harness Python AST 检查通过。

## 尚未解决的集成前置与限制

`Outbox.LockEpochs` 要求 `auth_email_identity_key_epoch` 已存在，而 `challenge.Store.Issue` 的懒初始化发生在其后。当前 identity `Resolver` 只校验 epoch，没有显式初始化 API；测试夹具预置了 epoch。不能让 outbox 在缺行时静默初始化，因为生产库可能已有身份或挑战，重置 epoch 会破坏密文索引绑定。需要独立、串行的 identity bootstrap：检查旧身份/挑战状态、仅在确认安全时初始化，且缺失/不匹配时失败关闭。请 Lead 单独决定并实现；当前空库首签发链路仍受阻。

code-key bootstrap 虽有数据库级幂等与历史行保护，仍要求启动前停止旧进程，并由受控运维流程串行调用。多个新实例并发使用同一密钥可安全收敛；不同 key 的竞争只允许数据库胜出的值，其他调用失败，不能用于在线轮换或密钥恢复。

worker 在 SMTP 前检查租约并传入 deadline，但数据库和 SMTP 接受之间无法原子提交。若租约恰在检查后到期、邮件服务已接受而响应迟到，结果只能保守记为 `unknown`，邮件仍可能到达；更换 slot 后已在途邮件也可能迟到，挑战消费仍必须检查当前 slot。测试使用 outbox sender shim；SMTP TLS sink 在现有 `mail` 包测试中独立验证，未形成真实 outbox→SMTP 的端到端连接。

未接入 HTTP/登录路由、生产 worker 调度、供应商配置或真实邮箱；无生产数据库或手机验证。未改迁移内容或共享清单。

## 失败记录

首次沙箱 MySQL 初始化失败并保留目录 `/private/tmp/nerae-email-outbox-mysql-ltrkv85a`。早期 race 运行因测试夹具重复初始化 epoch 失败，保留 `/private/tmp/nerae-email-outbox-mysql-7dopn6iv`。epoch bootstrap 测试首次留下 pending 夹具行，导致并发领取测试失败，保留 `/private/tmp/nerae-email-outbox-mysql-pu1ia2ly`。随后两次完整 harness 在并发领取用例报告 `ErrNoPending`，目录分别为 `/private/tmp/nerae-email-outbox-mysql-mon8wiar`、`/private/tmp/nerae-email-outbox-mysql-b7uinm6t`。失败未被隐藏：原测试用两个独立 channel 分别传 claim/error，存在结果错配的测试缺陷可能；`SKIP LOCKED` 的瞬时空轮询也是待验证猜测，不能据此确认生产根因。测试改为原子传递 claim/error，对瞬时空轮询只在新事务有界重试并仍要求两个不同记录都最终领取。之后完整 harness 连续 3 次通过；失败目录均未清理。

## SHA-256

```text
3da5bb0fe86d33bb004b616fa218f02e596adc47572f7bf504c94f16d01571f5  apps/api/internal/modules/auth/issuance/outbox.go
48ee5a803b9e675fa37d4b3be58c50edb4b061afd863ad921ef63ccf045b1993  apps/api/internal/modules/auth/issuance/outbox_test.go
5daba58a1c38c871bf0e6b477ed8d802df3e6e3639a55dc1b2df15e4a3260994  docs/evidence/A01/A01-13/mysql_smoke.py
4c0ac773cf29977aa46ab54647a82473a4aa234922b6eaee91342e5580a5a80b  apps/api/migrations/auth/0007_email_outbox.up.sql (只读核验)
```

## 2026-10-30 身份初始化和 SMTP 实际接缝

Lead 在合入身份 bootstrap 基础后的隔离工作树，将 outbox 夹具从直接写身份 epoch 改为使用 `bootstrap.IdentityEpochInitializer`；独立 MySQL 同时应用 user、邮箱身份与 outbox 迁移。新增一个专用测试数据库，执行真实 `Outbox.SendOne` → `mail.SMTPMailer` → 本机 loopback TLS SMTP 接收器，校验收到的合成收件地址、派生验证码、邮件协议和状态：服务器明确接受时落库 `accepted`；接收 DATA 后断开不回执时落库 `unknown` 且不自动重试。TLS 通过仅用于测试进程的临时自签测试根证书做证书校验，没有关闭证书或主机名验证。没有连接真实供应商或邮箱。

`python3 -B docs/evidence/A01/A01-13/mysql_smoke.py` 授权环境退出 0：两个新建的 socket-only 合成数据库、该端到端测试、其余 outbox race 和本机 SMTP 测试通过。成功后仅移除本次专属实例目录。首次尝试用 `python3 -m py_compile` 检查脚本时，系统 Python 默认缓存目录不可写而退出 1；改为 `python3 -B` 的 AST 解析后通过。

本节更新上文“没有 identity bootstrap / outbox 到 SMTP 端到端测试”的当时状态：现在基础已在隔离测试中接通，但服务启动装配、挑战 HTTP、匿名幂等、队列 worker、真实 SMTP 配置和邮件投递仍未完成。当前该版本 `outbox_test.go` SHA-256 `c5e53d2cc3c5a1f91f67e267c5e15eac9e0ebff5c7db49e3fca01fd38e7aaec0`；`mysql_smoke.py` SHA-256 `eacf4e6aa14e9ee0c377a683724ebd06d60b4b64b6514476cb1081a4378909e2`。
