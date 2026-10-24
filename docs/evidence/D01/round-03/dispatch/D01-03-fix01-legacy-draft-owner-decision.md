同一fix01/EA20归属边界澄清，请只修这一处再集中交回，不扩UI或功能。

Lead静态复核当前normalizeConversationRecord与新增“旧未归属草稿迁移给单一参与者”测试：现在用parsed.actor作为fallbackDraftOwner，把旧raw.draft填入该方drafts并标draftOwnerInferred。新测试甚至设草稿“切换到教练前的旧草稿”、raw.actor=coach，再期待coach获得原文。这会固化刚复现的泄露：最后导航到教练页面不能证明草稿是教练写的。此前实际CUA就是学员写草稿→仅切教练，已有原文和证据；不需要再猜。

接受边界：没有可信owner的旧raw.draft应按未归属原文单独保留（例如unassignedLegacyDraft或等价），不进入任一方composer/list/摘要。已生成draftOwnerInferred的值也仍是未确认归属，不得当成已知owner。新写入的双方草稿正常按参与者隔离、保存、切换、刷新、发送只清本方；不要抹掉已知新草稿。无法自动恢复旧归属在fix01报告如实列边界，保留可恢复原文即可，本轮不要求新增归属选择界面。

请把该测试改成验证“未知未分配、原文保留、两方不可见”，不要用当前actor/页面替代作者证据，也不要清存储/reset。属于原EA20恢复与隐私规则，不是新增业务。保留原已修项目，完成一次最终类型/构建/测试/实际浏览器复核、新报告后自然交回供Lead冻结。
