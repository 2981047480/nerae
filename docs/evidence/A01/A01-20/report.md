# A01-20 匿名登录幂等回执加密基础

## 状态

`partial`。加密原语、跨进程密文解析和限定的错误回执通过包级 race 测试；任务仍未接入数据库/事务，不构成登录重放或凭据释放。

## 变更

- `apps/api/internal/modules/auth/loginreceipt/receipt.go`：实现三把互不相同且非全零的 32 字节密钥配置；长度前缀与独立域分离的幂等键 HMAC、请求 HMAC；规范 raw base64url key 校验、挑战 ID/验证码/ES256 compact proof 的大小和编码校验；版本化 AES-256-GCM nonce+ciphertext；固定长度 scope、两个 HMAC、challengeId、结果类型/引用与精确 UTC 微秒到期的 AAD。
- `ParseSealed` 检查 blob 版本及最小/最大边界并复制输入，供新进程从持久化字节重建封装值；它不认证密文，仍必须由 `Open` 做 GCM 验证。
- 增加 `ResultRejected`：其结果引用必须等于对应 challengeId；拒绝回执只允许空明文，因此不能承载成功凭据，结果类型本身受 AAD 认证。此分类不能替代事务协调器的验证码复验。
- HMAC/AES 接收的 key 在构造时复制。原始请求、密钥、Binding、上下文、密文及解密 Plaintext 的格式化、JSON、slog 表示均脱敏；错误仅返回固定分类。密钥的随机性/熵由受保护配置来源负责，本包只验证长度和相互不同。
- `apps/api/internal/modules/auth/loginreceipt/receipt_test.go`：覆盖密钥缺失/长度/全零/重复、规范幂等 key、请求字段摘要绑定、proof 编码/边界、AAD 篡改与跨请求替换、密文逐字节篡改、错误 AES key、nonce 唯一、随机源失败、UTC 微秒到期、输入/输出上限、跨进程重建及脱敏；零值 Crypto 的 AEAD、摘要和解密操作失败闭锁。

## 验证

- `go test -race -count=1 ./internal/modules/auth/loginreceipt`：退出码 0。
- 最终复验同一 race 命令在补充 `ParseSealed`、拒绝结果类型及全零密钥保护后退出码仍为 0；`gofmt -d` 无差异，`git diff --check` 退出码 0。
- Go 1.27.3，通过本轮指定的 `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off` 及隔离 module/build cache 执行。
- 初次 race 执行发现测试夹具 challenge ID 长度为 21 字节，不满足 OpenAPI 最小 22；修正夹具后重新执行通过。实现未因此放宽合同。
- `gofmt -w` 完成；未修改迁移、OpenAPI、request.go、HTTP、登录或其他模块。

## 安全边界与未完成项

`Open` 只做 AES-GCM 验证/解密。未来事务协调器必须在同一事务中先验证原设备签名、当前授权/撤销状态、过期与防重，再按提交结果控制凭据释放；这些复验、数据库持久化、提交门槛、密钥生命周期/轮换和真实登录均未实现或验证。解密 API 只供未来受信任内部协调器调用，不得直接暴露给 HTTP/客户端。

本原语的明文上限为 8192 字节；正式接线需结合实际响应和合同字段约束确认，不能假定所有合同允许的完整响应都能装入该上限。
