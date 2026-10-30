# A01-12 邮箱验证码派生基础

状态：`partial`，供 Lead 安全复核；没有接入挑战仓储、队列、发信、HTTP 或登录流程。

基线为 `cfeea214009c9574d7685621b160d710d225ca7a`，目标写域仅有 `apps/api/internal/modules/auth/issuance/` 与本证据目录。实现新增一个仅由构造器创建可用的 `Deriver`：要求独立提供恰好 32 字节密钥并复制保存；零值和 nil 均失败关闭。派生输入限制为 22–128 个 ASCII base64url challenge ID、`email_login` 或 `email_link`、6–10 位数字。HMAC-SHA256 输入含版本域分离、challenge/purpose/位数/counter 的长度前缀；每个 HMAC 候选取前 64 位大端数，以 `threshold = (-n) % n` 拒绝低值，最多尝试 64 次，再取模并左补零。相同 key 和输入可重复恢复。`KeyEpochTag` 使用另一个域分离产生非秘密 32 字节标签。

长度不能证明密钥熵。该模块不发邮件、不保存挑战或验证码、不做 key epoch 数据库持久化、轮换、限流、过期、一次性消费、身份认证或授权。派生码可由持有密钥和 challenge 上下文的一方重建，生产接入必须将高熵独立密钥保存在受保护配置中，并将 challenge 与服务端记录绑定；不能据此声称 SMTP/登录已接通。

## 验证

- `GOPROXY=off GOSUMDB=off GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -mod=readonly -race -count=1 ./internal/modules/auth/issuance` — exit 0，`ok nerae/api/internal/modules/auth/issuance 1.680s`。
- `/private/tmp/nerae-go-toolchain-1.27.3/go/bin/gofmt -d apps/api/internal/modules/auth/issuance/email_code.go apps/api/internal/modules/auth/issuance/email_code_test.go` — exit 0，无格式差异。
- race 测试覆盖合成密钥固定向量与 epoch 标签、构造器复制、相同输入重建、固定挑战/用途向量分离、挑战 ID/用途/码长/密钥长度边界、零值/nil 失败关闭、拒绝阈值下方候选并接受阈值候选、uint64 最大候选、64 次上限和候选错误、格式化/JSON/slog/验证错误脱敏。
- 首次 race 尝试因测试中选用的“无效”challenge ID 实际符合输入语法而失败；将用例改为包含空格的无效 ID 后，最终命令重新运行并通过。实现本身无需因此改变。

测试中的密钥与挑战均为合成值；固定向量用于锁定算法，不代表 6–10 位有限码空间内的不同输入在数学上绝不碰撞。没有启动数据库、发送邮件或调用外部服务。

## 修订和哈希

当前 HEAD 仍为 `cfeea214009c9574d7685621b160d710d225ca7a`；工作树未提交，新增文件限于任务写域。

| 文件 | SHA-256 |
| --- | --- |
| `apps/api/internal/modules/auth/issuance/email_code.go` | `aec237f01dc74d4c2135bf27e384a088010eef5b9f0e8e4ccc10a0dd15d96142` |
| `apps/api/internal/modules/auth/issuance/email_code_test.go` | `ef3d26244cff07bb726b1218ad7c5509811ac802893515428ae2890058bbac76` |

## Lead 安全复核

发现并修复了导出 `Deriver` 零值可使用全零密钥的缺口，构造器以私有初始化标记区分零值。Lead 用独立 Python `hmac` / `hashlib` 重新计算固定验证码与 epoch 标签，向量一致；在该工作树复跑 `go test -race -count=1 ./internal/modules/auth/issuance` 退出 0。验证码派生不持久化码或可逆码，但不替代高熵独立密钥管理；后续挑战接线须明确修订当前 `IssueInput` 对随机来源的说明，并在生产使用前实现数据库 epoch 检查和轮换策略。
