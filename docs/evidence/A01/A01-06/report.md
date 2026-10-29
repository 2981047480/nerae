# A01-06 · 用户令牌签名与验证基础

状态：partial，未接受。Lead 基于 `d4fa395` 增加仅供内部使用的用户 JWT 签名与严格解析；固定 HS256、用户 issuer/audience，要求外部提供 32 字节高熵密钥，不预置真实或默认密钥。签名值、解析结果和密钥配置在 JSON、结构化日志和格式化输出中脱敏。此组件不会建立登录会话、查询数据库或授予资源访问。

`cd apps/api && GOTOOLCHAIN=local GOPROXY=off GOCACHE=/private/tmp/nerae-go-api-cache GOMODCACHE=/private/tmp/nerae-go-api-mod /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race -count=1 -v ./internal/modules/auth/session` 退出 0。覆盖签名与错误密钥、令牌篡改、算法和关键扩展头、重复或缺失字段、错误 issuer/audience、用户与管理端类型隔离、到期边界、时钟偏差边界、期限上限、数字溢出、非法主体和日志脱敏。只读安全复核未发现可构造的签名绕过或算法混淆；其提醒的时间/数值边界负例已补充并重跑。

被测文件 SHA-256：

- `session/token.go`：`335c4a3b0d90113f2cf7dabddc21d2762aa23c0917a545c85c68bf9822c15253`
- `session/token_test.go`：`9474dc8d5a013481c425ea5feb5a1b13861d768f0a86238fe430a7dc9e15b5ab`

这是密码学基础，不得单独作为认证中间件。后续每次请求都必须在业务事务中检查数据库会话、主体、身份、可信设备及 token version 的当前状态，并校验资源归属和年龄门槛；签发必须源自真实已验证挑战、随机会话 ID 和已提交事务。密钥运维与轮换、设备批准/恢复、会话撤销、幂等响应和真实邮件登录均尚未完成。
