# A01-01 设备证明校验进度

状态：partial。实现独立的 P-256 / ES256 设备持有证明校验；未接入认证 HTTP、短信、可信设备或会话签发，不授予业务访问。

- 校验服务端挑战绑定的公钥、算法、用途、资源、nonce、签名与有效期；拒绝重复 JSON 字段、未支持的关键扩展和非法公钥。错误不回显凭据。
- NumericDate 使用有理数比较；返回的 `ReplayUntil` 向上取整并覆盖允许的最大时钟偏差，避免后来增大时钟偏差时过早清理防重记录。
- `JTI` 的原子持久化消费仍由后续身份事务实现；本函数无状态，因此重复调用可能再次通过，不能称为完成防重。

`cd apps/api && GOTOOLCHAIN=local GOPROXY=off GOCACHE=/private/tmp/nerae-go-api-cache /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race ./internal/modules/auth/...`：exit 0。测试使用临时生成的 P-256 密钥，覆盖正确签名及错误签名、公钥、算法、绑定、时间、重复字段和重放保留边界。独立检查未发现可复现缺陷；最终安全验收仍待真实事务、原生设备密钥与端到端联调。
