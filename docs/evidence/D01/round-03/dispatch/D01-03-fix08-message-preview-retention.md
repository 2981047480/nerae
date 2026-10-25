# fix08：消息状态恢复不能删除会话

同一 Harness / Flash Medium / 原树4187，基线 `29bddb694e1498549aaa20cbc24511c9d45a515e`。不改HEAD，所有固定QA端口含4192保留。**只修这处C06恢复过滤及有效回归，不重做UI/样式/路由/其他功能。**

fix07第14轮正常30m41/966步交回，Lead核对60源/10证据/2报告；独立类型/构建/111测试/原28接缝全过，真实Core8组、旧事实保留、同单位图表已过。Lead已在固定29完成220尺寸图、96隔离状态和24张正常/极窄大字原图目视检查。不要重做已通过功能。

实际两个UI动作复现：学员顾言会话输入草稿 → 预览选择发送失败 → 再选择正常，顾言整段会话消失，消息列表只剩江遥/何岸，草稿也消失。正常恢复不等于重置数据。证据：`lead/expansion/frozen-08/flows/message-failed-mode-with-private-draft.*` 与 `message-normal-after-failed-mode.*`。

根因已定位 `store.ts:applyPreviewState` 的messages/normal：对`state.conversations`执行filter，删除了包含preview-pending的整段会话；本意应仅清除相应样例占位消息。保留真实会话ID、历史消息、双方私稿、对方身份和已通过的独立未读规则；不能用重置/删全部消息/重建seed来遮盖。

另当前ensurePreviewMessage固定author=student，所以教练发送中/失败的预览模式需实际发送本人的消息才能见到本方状态。此条不是要求扩展作者逻辑；真实发送/同ID重试由Lead继续走查，避免扩大修复。

验收：有效正反例覆盖有/无占位消息、失败/发送中回正常、双方真实私稿与旧消息保留、别的会话不变；针对性390实际UI切换/刷新；原111+新回归及type/build全过。fix08报告md/json与source/evidence/report三SHA清单，排除dev日志后自然正常交回。候选不自标accepted。只恢复过滤，不引入新功能/新文案或其他重构。
