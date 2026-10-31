# RecordingApi 幂等有效期回执 · 2026-10-31

基线为集成提交 `132fa92a2c3f01888617c6536fba548308d721ba`。`ScopedSessionDetail` 在成功的创建、组批次保存和完成写入结果上可选返回 `idempotencyKeyExpiresAt`；仅 GET 的详情和列表结果不会带此属性。值必须来自受控 `RequestPort` 的 `Idempotency-Key-Expires-At` 响应头，客户端不计算或补造期限。

验证器只接受合法日历日期、有效时分秒和 UTC RFC3339 时间（`Z` 或 `+00:00`，最多 9 位小数）。它保留头值原文，避免丢失纳秒精度；不比较客户端时钟，即使测试回执时间已过去也只作为不透明的服务端期限传递，不据此声称仍可重放。成功写入响应缺少该头、头名大小写重复或值无效时返回固定 `reconcile_required`，因为请求可能已经提交；不会自动重试。uni `request` 端在成功 POST 遇到不可解析/重复允许头时映射为该错误；App 原生 transport 若在 API 边界前因无效响应头拒绝回调，则相同的保守错误映射也会要求 reconciliation。GET 忽略任何同名头。其他响应头（包括 Cookie、Authorization、请求标识）不进入 API 结果。

## 验证

| 检查 | 结果 |
|---|---|
| `node --import ./docs/evidence/P02/P02-11/node-ts-loader.mjs --test apps/client/src/features/training/recordingApi.test.mjs` | 41 项通过；包含创建、组写入、完成、GET、缺失/非法/重复、旧时间、native 风格回调失败和会话切换 |
| `vue-tsc --noEmit -p <临时副本>/apps/client/tsconfig.json` | exit 0；将 `apps/client` 复制到新建临时目录，并临时链接集成树中已有的锁定 `node_modules`，未安装依赖；临时目录在检查后清理 |
| `git diff --check` | exit 0 |

类型检查直接以本工作树运行时因缺少 `@dcloudio/types` 失败；以上临时副本检查使用相同集成版本依赖通过。测试均是 Node 合成 transport，没有真实 App、小程序、线上 HTTP 或故障恢复验收。

此改动只传递并格式校验服务端有效期声明，不是持久待发队列或重放实现。有效期值仍不意味着响应正文已经过端到端结算；后续 journal 必须另行绑定原请求、主体、响应和提交状态，并不得依赖本机时间推断服务端缓存仍存在。
