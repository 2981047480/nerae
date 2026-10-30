# A01-16 邮箱挑战匿名幂等与限流接缝

## 状态

`partial`，待 Lead 安全复核。基线 `2419d2ef50513dc9bb5a98dc9345e6e516005122`。仅新增内部 `ChallengeRequestIssuer`，未挂 HTTP，也不创建账号、验证登录或发送邮件。

## 实现

- `Issue` 固定 `POST /auth/email/challenges`、`email_login`。邮件地址先规范化为受保护索引；公钥必须通过 P-256 JWK fingerprint 校验。幂等键采用严格 canonical base64url，长度 22–128 字符且解码至少 16 字节。调用方仍须用 CSPRNG 产生幂等键；运行时只验证格式和长度，无法证明熵。源 IP 由调用方作为 `netip.Addr` 提供；本层不解析也不信任 forwarding header。可选 `deviceLabel` 用指针区分省略与存在，UTF-8 字符数限 1–80。
- 每次请求先执行 `ratelimit.EmailChallenge` 原子检查；同键重放也消耗 email/device/IP/global 限额。Redis 失败返回不可用且不进入 SQL 写入。没有读取账号是否存在的逻辑。
- SQL 锁序为 outbox identity/code epochs → request-key epoch → email slot → request row → challenge → outbox。slot 通过 CSPRNG 候选 ID 的 no-op duplicate upsert 串行化；已有 slot 保持原 challenge。重放检查发生在任何挑战推进之前。有效期在取得 slot/request 行锁后读取数据库 UTC 时钟；解密后、提交重放前再次核对 request 与 challenge 截止时间，新响应保留期在写入前按数据库时钟计算。新挑战、outbox、请求 HMAC 和 AES-GCM 响应同一事务提交；只有 commit 成功才返回 WireBody。提交错误返回 `ErrChallengeRequestUnknown`，不给调用方回执。
- request-key epoch 由显式 `InitializeKey` 初始化；已存在时只接受同 key ID/tag，缺行只有在 request 历史为空时才插入。请求键 HMAC、请求内容 HMAC、响应 AES-GCM 使用分别配置的 32 字节密钥；key epoch tag 覆盖三把密钥。保留期显式配置并写入每条记录。挑战码长度为服务器配置的 6–10 位，不是请求输入。
- 请求摘要按固定字段顺序进行语义化 length-prefix HMAC，不哈希原始 JSON 字节：固定 method/route/purpose、受保护 email index、输入 email 原始表示、公钥 fingerprint、label 的 presence 与 value。原始 email 和 label 只参与内存 HMAC，不写入 SQL。不同 JSON 字段顺序因此语义一致；同幂等键改变 email 表示、公钥或 label 均冲突。
- 重放必须确认 request HMAC 相同、记录未过期、active slot 仍指向该挑战、挑战未过期/消费/耗尽、受保护邮箱和派生码摘要仍有效、outbox 行存在；响应通过绑定 email index、幂等键 HMAC、请求 HMAC、challenge ID 和 key ID 的 AEAD 解密并核对响应结构、nonce 与挑战过期时间。失配、密文篡改及数据库错误失败关闭。

## 验证

最终命令：

```sh
python3 -B docs/evidence/A01/A01-16/mysql_redis_smoke.py
```

退出码 `0`。脚本新建本次专用私有目录，启动禁 TCP 的 Unix socket MySQL、仅 Unix socket 且无 RDB/AOF 持久化的 Redis，应用独立合成数据库迁移，并运行：

```sh
/private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race -count=1 -v ./internal/modules/auth/issuance
```

`GOTOOLCHAIN=local`、`GOPROXY=off`、`GOSUMDB=off`；只运行离线缓存。A01-16 同键并发精确重放、同邮箱不同键并发串行与仅 active challenge 可重放、同键不同公钥/email 表达/label 冲突、另一邮箱相同键隔离、重发后旧挑战不可重放、消费/过期/耗尽、缺失 outbox、响应密文篡改、触发器中途失败回滚、Redis 失败关闭、四维限流、验证码 10 位配置、epoch 丢行拒绝，以及锁等待跨过幂等保留期后不重放旧结果均通过。重复初始化同 key 成功、不同 key 和有历史记录时缺 epoch 均失败关闭。失败场景没有返回响应正文。

commit-ack-loss 用真实 MySQL 测试连接执行提交后，由测试专用 driver 注入“服务端提交成功但客户端收到错误”；断言 `Issue` 返回未知结果且无响应，普通连接使用同键重试可恢复同一响应且只有一条 challenge/outbox/request 记录。它验证提交成功后回执丢失的重试语义，不代表网络分区下所有数据库提交结果都已实测。

Go 离线包编译、`go vet ./internal/modules/auth/issuance`、`gofmt -d` 和 Python AST 检查通过。完整 harness 中既有 outbox/SMTP 测试因其专属 DSN 未配置而按原测试逻辑跳过；本卡不调用 sender。真实 SMTP/外网没有连接。

失败现场均保留且没有清理：最初 epoch 丢行测试顺序问题 `/private/tmp/nerae-a01-16-2ggwd935`；局部变量声明编译错误 `/private/tmp/nerae-a01-16-wd8zcbbx`；锁等待测试误期望 request 行数为 2 `/private/tmp/nerae-a01-16-xd598odh`。各问题修正后最新完整 harness 退出 `0`；成功 harness 仅清理各自本次创建目录。一次辅助检查命令因从 `apps/api` 使用了仓库根相对路径而退出 `1`，随后从仓库根重跑 AST 检查通过。

## 未验证 / 集成限制

- HTTP handler、JSON schema/重复字段/未知字段校验、可信代理 IP 解析、错误码映射、`Cache-Control: no-store` 及路由装配均未实现。公开接线仍须按 OpenAPI 严格验证 body（包括可选 `deviceLabel`）、拒绝未知/重复字段，并将已验证字段显式映射到内部输入。
- 同一挑战的外部 SMTP 投递状态仍由 A01-13 outbox worker 管理；本卡只原子写入队列，不表示邮件已送达或登录成功。没有真实账号、SMTP、生产密钥、生产 Redis/MySQL或手机测试。
- commit acknowledgement loss 是可控 driver 注入场景；真实网络分区/数据库故障下的提交结果不确定性没有端到端复现。
- 密钥仅验证长度、角色值不重复和数据库 epoch；密钥熵、密钥管理系统、轮换/备份和运维停止旧 writer 的流程不在本卡验证。

## 基线、范围与摘要

工作树 HEAD 保持基线 `2419d2ef50513dc9bb5a98dc9345e6e516005122`，未提交。只新增任务写域 `request.go`、`request_test.go` 与本目录；A01-16 DDL 保持 Lead 提供的只读文件，SHA-256：`de3f462bea251da845b37a9cf5ba60d4636b8b4870e74ace3ff9173d034ae8c4`。


## SHA-256

```text
002c685de833c212d44f33791ecb1d1d47094b230c978c03f0688dd8a045e969  apps/api/internal/modules/auth/issuance/request.go
681b39bea254f1eaec30c308ef9feb5d2768e84f03c2ae57d5f4866d4241951f  apps/api/internal/modules/auth/issuance/request_test.go
567660d9d70386164e0d2bac404064944993f10e8577cece9732e9dc5e50fa51  docs/evidence/A01/A01-16/mysql_redis_smoke.py
de3f462bea251da845b37a9cf5ba60d4636b8b4870e74ace3ff9173d034ae8c4  apps/api/migrations/auth/0008_email_challenge_request.up.sql (只读核验)
```

## Lead 集成复核

- 核对请求摘要覆盖原始邮箱表示、已验证设备公钥及可选设备提示；锁等待后重新取数据库时间，并在重放前复核两种过期时间。说明：本卡的 `deviceLabel` 只用于请求一致性校验，尚未持久化或在可信设备确认页面展示，不能宣称设备提示已实现。
- 集成工作树复跑 `python3 -B docs/evidence/A01/A01-16/mysql_redis_smoke.py`：退出 0；其中本卡 MySQL/Redis 用例实际运行，其他 outbox 专用数据库测试因没有配置其单独 DSN 而跳过。测试服务仅监听本机 Unix socket，成功后只删除本次新建临时目录。
- 迁移清单校验通过，包含 13 条迁移。第一次迁移清单编辑从错误目录执行而未写入，随后检查正确拒绝未登记的 0008；从仓库根登记后重新检查通过。该失败没有修改真实数据库。

集成版本 `8dc2541`：完整 `go test -race -count=1 ./...` 通过；未配置各自独立 DSN 的数据库用例按设计跳过，A01-16 数据库和 Redis 用例以本报告前述隔离实例实测为准。静态合同检查在项目原始 Python 3.11 环境运行 99 项，通过；集成树内约定的 `api/tests/.venv-py311/bin/python` 不存在，首次尝试返回 127，改用原始项目的同版本环境后通过。
