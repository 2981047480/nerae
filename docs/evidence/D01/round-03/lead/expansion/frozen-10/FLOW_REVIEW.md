# F01–F07 · Lead 实际流程复核

候选898e3546363cf7348b6599601b2570597a14820c；角色为产品/交互/QA顺序复核。仅本地样例，不代表真实训练、消息、服务、审核或健康结果。

| 流程 | 实际操作与结果 | 证据 |
|---|---|---|
| F01 找内容并开始 | 708实际匿名完整浏览、筛选回退、登录取消、换内容仍要求成年、未成年阻止、替换取消保留3组；898实际B日4动作13组，开始/冷恢复仍B日高位下拉。最终5种详情状态重扫 | ../frozen-09/flows/f01-final-results.json；flows/f01-multiday-final-results.json；states/state-results.json |
| F02 回来训练并记录 | 898实际默认12组（原3+新9）、Core8组、Mobility6组；负重/每侧/秒/自重/呼吸、冲突选另一条仅一次、真实组配对与旧0kg保留。708真实暂停/冷恢复/加15秒/继续，双模式时钟单调递减 | flows/ea37-targeted-results.json；../frozen-09/flows/f02-final-rest-results.json；../frozen-09/motion/training-motion-final-results.json |
| F03 交付与采用 | 898队列两次导航接续；保存8/冷恢复，发布取消留7，确认8但现用6；先看周岚再打开林澈旧7，旧内容/只读/冷恢复/返回保持；新8明确取消/确认/冷恢复，今天3与历史值保持 | reference-flow/three-stage-results.json；code/identity-seams.json |
| F04 双方具体沟通 | 708双方各自草稿、失败不对方可见、同条重试到达一次；教练本人发送中可见且对方未收；失败/发送中回正常不删会话/私稿。撤权取消与确认，本人历史在，教练冷消息/待办/列表/详情健康原文不可见。898双方7状态及30模块接缝重过 | ../frozen-09/flows/f04-final-results.json；../frozen-09/states/message-own-send-supplements.json；../frozen-09/flows/retention-results.json；states/state-results.json；code/identity-seams.json |
| F05 服务与咨询 | 708负价格阻止；759/项/8周/12h保存冷回读与实际学员详情同步，已有699/月/4周/v2不被改写。选择单次包带入咨询私稿，冷恢复且未自动发送；该教练原已有关系，不把这次咨询算新成交。898详情/服务全状态与渲染重过 | ../frozen-09/flows/f05-final-results.json；states/state-results.json；ui/matrix-results-pixel-verified.json |
| F06 有据候选 | 708取消仍10次、确认变个人草稿8次；冷恢复后同候选禁用，当前3/12未动；教练来源只进入草稿边界。898七状态与实际模块重过 | ../frozen-09/flows/f06-final-results.json；states/state-results.json；code/code-checks.json |
| F07 自有内容与观察 | 708公开/私密字段保存冷恢复；公开弹层仅公开字段，修改95秒后新版本5完整重审；确认后仍待审核/仅自己。饮食部分按计划+观察冷恢复，撤权仍保留本人观察；无热量或效果推断。898对应15状态与公开字段模块重过 | ../frozen-09/flows/f07-final-share-results.json；../frozen-09/flows/f07-final-diet-results.json；states/state-results.json；code/code-checks.json |

跨版本使用有边界：898相对708只有RecordsView摘要调用、format新增纯函数和测试三个文件变化；原format导出、store、其他视图、导航、全局样式均原样。见[source-change-scope](source-change-scope.json)。未将708截图伪标为898；记录页全部实际重走，F03也在898完整复验；最终全部22构图/96状态均源898。集成后另跑同版本代码检查与浏览器接缝。

用户决定目标、是否开始/替换、实际完成值、服务适合性、采用与公开；App带入内容、当前对象、恢复、保存和结果；模板只提供有来源固定候选。观察到的动作预算：今天→当前会话1次；默认一组确认1次（需要时修改实际值）；教练队列→完整草稿2次；带包咨询选择后1次进入并预填；重大替换/采用/发布/分享保留明确确认。这是Lead走查，未做真实用户计时或偏好实验。
