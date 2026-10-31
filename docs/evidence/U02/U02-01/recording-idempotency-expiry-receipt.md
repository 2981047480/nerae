# RecordingApi 幂等有效期回执 · 2026-10-31

基线为集成提交 `132fa92a2c3f01888617c6536fba548308d721ba`。`ScopedSessionDetail` 在成功的创建、组批次保存和完成写入结果上可选返回 `idempotencyKeyExpiresAt`；仅 GET 的详情和列表结果不会带此属性。值必须来自受控 `RequestPort` 的 `Idempotency-Key-Expires-At` 响应头，客户端不计算或补造期限。

验证器只接受合法日历日期、有效时分秒和 UTC RFC3339 时间（`Z` 或 `+00:00`，最多 9 位小数）。它保留头值原文，避免丢失纳秒精度；不比较客户端时钟，即使测试回执时间已过去也只作为不透明的服务端期限传递，不据此声称仍可重放。成功写入响应缺少该头、头名大小写重复或值无效时返回固定 `reconcile_required`，因为请求可能已经提交；不会自动重试。uni `request` 端在成功 POST 遇到不可解析/重复允许头时映射为该错误；App 原生 transport 若在 API 边界前因无效响应头拒绝回调，则相同的保守错误映射也会要求 reconciliation。GET 忽略任何同名头。其他响应头（包括 Cookie、Authorization、请求标识）不进入 API 结果。

POST 已发出后，传输丢失/超时/取消、5xx、意外成功状态、无法解析的正文、schema/投影不符均返回 `reconcile_required`，而且没有自动重试。会话在途切换仍返回 `session_changed` 且不释放旧响应；这不证明旧 POST 未提交。后续协调器必须把该操作保留为待核对，保留原请求体、幂等键和原会话身份；不得因 `session_changed`、`network` 或 `invalid_response` 新造 key 重发。已知 4xx 保持合同分类：401/403/404、409/412、400/422、429 分别表示认证/权限/资源、冲突/版本前置条件、输入校验和限流结果；不自动重试。这个适配器本身没有持久 journal 或恢复协调器。

## 验证

| 检查 | 结果 |
|---|---|
| `node --import ./docs/evidence/P02/P02-11/node-ts-loader.mjs --test apps/client/src/features/training/recording.test.mjs apps/client/src/features/training/draftStorage.test.mjs apps/client/src/features/training/recordingApi.test.mjs apps/client/src/features/training/recordingProjection.test.mjs` | 103 项通过；包括过期回执、创建/组/完成不确定响应与一次发送约束、十进制精度边界，原 projection/WeakMap 与草稿用例通过 |
| `vue-tsc --noEmit -p <临时副本>/apps/client/tsconfig.json` | exit 0；将 `apps/client` 复制到新建临时目录，并临时链接集成树中已有的锁定 `node_modules`，未安装依赖；临时目录在检查后清理 |
| `node --check docs/evidence/A04/A04-09/client_write_interop.mjs` | exit 0 |
| `python3 -B docs/evidence/A04/A04-09/mysql_smoke.py` | exit 0；本机 socket-only 私有 MySQL、真实 HTTP/TLS 与 Node `RecordingApi` 联测通过，创建/同键重放/组写/完成回执原文相同，GET 不带回执。测试仅合成主体及会话 |
| `git diff --check` | exit 0 |

类型检查直接以本工作树运行时因缺少 `@dcloudio/types` 失败；以上临时副本检查使用相同集成版本依赖通过。四组 Node 测试使用合成端口；A04-09 的另一次测试则通过本机 TLS 与私有 socket MySQL 实际收到了服务端头。两者都没有真实 App、小程序、线上 HTTP 或故障恢复验收。

此改动只传递并格式校验服务端有效期声明，不是持久待发队列或重放实现。有效期值仍不意味着响应正文已经过端到端结算；后续 journal 必须另行绑定原请求、主体、响应和提交状态，并不得依赖本机时间推断服务端缓存仍存在。

集成提交 `7ffce4f` 的五组训练测试（含安全存储适配器）共 123/123 通过，`pnpm --filter @nerae/client run typecheck` 退出 0。`PYTHONDONTWRITEBYTECODE=1 python3 -B docs/evidence/A04/A04-09/mysql_smoke.py` 在私有 MySQL、本机 TLS 与真实客户端适配器上退出 0，覆盖训练后批量、逐组写入、同键重放、完成与合成会话读回。第一次误写成不存在的 `mysql_http_client_smoke.py`，Python 退出 2 且未启动服务；已用仓库中的正确脚本完成验证。此结果仍不代表实际两台设备或完整持久重放。
