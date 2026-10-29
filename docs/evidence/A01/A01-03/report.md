# A01-03 · 测试邮箱发信通道进度

状态：partial，未接受。Lead 以 `b65ebe1` 为任务基线，实现可配置的隐式 TLS SMTP 发信组件；仅在证书和主机名验证成功后进行认证。邮箱与验证码不进入错误文本；配置和组件的常见格式化输出作脱敏处理。验证凭据、连接权限、限流、挑战单次消费及会话签发不在此组件内。

`cd apps/api && GOTOOLCHAIN=local GOPROXY=off GOCACHE=/private/tmp/nerae-go-api-cache GOMODCACHE=/private/tmp/nerae-go-api-mod /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race -count=1 -v ./internal/modules/auth/mail` 退出 0。测试只在本机临时回环 TLS SMTP 服务进行，使用测试证书及测试地址，不连接真实邮箱。覆盖成功、收件人拒收、DATA 后 4xx/5xx 明确拒收、确认响应丢失、QUIT 失败、证书不受信任及主机名不匹配、请求取消、TLS 握手超时、地址注入与配置脱敏。

只读复核发现明确拒收被误归为结果未知的问题；Lead 修复并重跑上述测试。发信地址校验随后改为复用身份领域的统一规则；又补充结构化日志脱敏及禁止配置 JSON 序列化的测试，邮件 race 测试重跑通过。被测文件 SHA-256：`smtp.go` 为 `5b05373896a5fc12f771c3dca2eda1e2186eb925a136fc8ae798dd672683cd89`，`smtp_test.go` 为 `2d9dc138c39140dec2db8b6857123e9c10c3ed5b5d54a196c1106a49d45e59f1`。

SMTP 返回成功只代表服务端接受，不证明用户收到。QQ 邮箱启用状态、授权码安全配置、实际发信政策和送达尚未验证；发信与认证事务、幂等、频率限制及真实客户端尚未接线。测试版仅接受合同允许的 ASCII 地址，实际语法继续由服务端校验。本任务不代表邮箱登录或同步可用。
