# 训练请求响应头传递 · 2026-10-31

训练请求端口增加可选的响应头字段。App 原生 HTTPS 适配器把已通过原生桥白名单处理的响应头保留下来；uni-app 适配器仅保留 `Content-Type`、`Cache-Control`、`X-Request-ID` 和 `Idempotency-Key-Expires-At`，拒绝同名大小写重复、非字符串和含控制字符的允许项，不向调用方传递其他响应头。此处保留头信息不代表它已被鉴权或足以证明写入成功；后续待发操作的结算仍须验证原始请求、服务端结果、主体和有效期。

在 `apps/client` 使用 P02-11 的 Node TypeScript loader，运行 `recordingApi.test.mjs`、`nativeHttpsTransport.test.mjs` 和 `recordingProjection.test.mjs`：退出 0。新增用例检查原生响应头传递、小程序端白名单及重复/非法值失败关闭。`pnpm --filter @nerae/client run typecheck`：退出 0。所用网络回调和 uni.request 均为合成端口，没有执行真实 App、小程序、线上请求或重启后的重放。

响应头可选：其他旧端口可能尚未返回它，调用方不能把缺失字段当成有效期或确切回执。平台可能在交付给 JS 前合并重复头；实际原生设备、小程序和真实服务端代理仍需联测。
