# D01-03 fix04 候选报告（待 Lead 冻结复验）

状态：**candidate / 尚未 accepted**。基线为 Lead 冻结 `55994603461ee5710a5be4f70b1180d445dba5c5`；本轮只收束引用返回与旧缓存迁移，未改 Git HEAD、产品范围、风格或固定预览日期，4186/4188/4189 均未触碰。预览 `http://127.0.0.1:4187/`，PID 64273。

## 处理

- **训练引用返回**：`openMessageRef(training)` 不再调用会 root/清栈的 `goToRecords()`，改为保留现有 stack 的引用导航。9/29 记录冷恢复仍展开原记录，`goBack` 回 Gu messages；直接主导航记录仍为 root。未知训练记录显式不可用、不展开默认 10/22。
- **旧 coach messages**：冷加载按原始 selection 字段存在性与合法 `parsed.actor` 迁移，不再被合并后的 base 默认 `selection.actor=student` 覆盖；旧 coach/周岚消息保留教练读者和周岚会话。
- **旧 incomingPlan**：缺 owner/version/readOnly/refMissing 的旧引用保留可信读者，从 active conversation 取所属学员并标记“引用不可用”；`incomingView` 为空、不能采用，不落任何学员 latest。无可信学员时 header 不再回落林澈。
- **旧 records 引用**：screen=records 且 stack 含 messages 但缺 focus 字段时，explicit=true、id=null 并显示“这条训练引用对应的记录不可用”；正常直接进入记录（非引用）仍默认展开最近 10/22，不一刀切。

保留 fix03 的 EA28/29 新格式开放→刷新→返回、周岚/林澈同号隔离、只读/历史禁止采用、缺失不 fallback，以及 VR13/14、M02/M04/M05。

## 验证

最新同版：typecheck **0**、build **0**、vitest **101/101**、exit **0**；日志 `logs/d01-03-fix04-*`。390 隔离浏览器实操：9/29 引用冷恢复/返回、旧 coach messages、旧 incoming 缺失、旧 records 引用与直接入口；证据 `fix04-*.png/json`。模块为 vitest/jsdom，浏览器为 CDP，均不等于 Lead 的冻结版 21 接缝或全矩阵验收。

## 散列与未测

源码清单 `logs/d01-03-fix04-source-sha256.txt`，sha256 `f0a8b428…`；证据清单 `logs/d01-03-fix04-evidence-sha256.txt`，sha256 `128996a6…`；dev 日志排除。未测：Lead 全 16 族/五宽/200/动效、真实服务端权限/消息/健康鉴权、微信/uni-app 真机，以及 M02/M04/M05 新渲染的 Lead 验收。完整目标仍 active，请 Lead 冻结后独立复验。
