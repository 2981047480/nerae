# D01-03 fix01 · EA19后续与EA20草稿归属

继续同一轮、同模型/树/写域，不换风格、不扩业务。Lead刚在实际4187/CUA做了小范围失败路径复查；这是当前变化中源码的缺陷证据，不是冻结接受。60份源在观察前后散列一致，聚合7ddb6ff6a04fa0decc68bf82e5eee7262e0b652502917e161cd25b905e5f85aa，证据绝对目录：
/Users/zephyr/Documents/项目/nerae/docs/evidence/D01/round-03/lead/expansion/fix01-interim/

实际操作：切学员→消息顾言→预览当前页“发送失败”→关闭→填写“未发送的安排：周六晚半小时”并发送（实际未发送）→另填“只属于学员的未发送草稿”，没有发送→仅切教练角色、普通导航消息→林澈。没有reset或重新授权。

- EA19后续：教练列表把未发送正文作为最新消息，会话也显示学员failed原文及重试/删除按钮。未读0已改善，但不能据此把未收到的内容交给对方。当前MessagesView直接遍历displayConversation.messages，lastMessage取raw最后一条；health投影没有覆盖发送状态。请让当前读取方只看到自己尚未成功的消息与操作；对方仅收到成功消息。列表摘要/时间/线程/引用/空态同源，sending与failed不得泄露给对方；重试/删除动作也须限本人，不能由接收方重试发送者的失败消息。同ID成功一次、不重计不复制。
- EA20：教练“写消息”输入框原样出现学员未发送草稿。conversation.draft被双方共用。草稿须按会话及参与者归属隔离；双方独立编辑、切换与刷新都各自恢复，发送一方只清该方草稿。旧的未归属草稿保留但不能复制给双方；无法可靠判定旧归属请如实报告边界，不能清存储/reset掩盖。

failed-coach-list.txt、failed-coach-thread-and-shared-draft.txt及同目录screenshots为实际证据；failed-student-with-own-draft.txt记录发送方。第一次学生截图滚动变化已明确excluded，retry及教练截图前后390×900/DPR1/scroll稳定。不要更改Lead证据/脚本。

这些仍属原S08/F04正常、失败、草稿与参与者边界。请在当前fix01交回前集中修复并补有意义正反例、真实浏览器检查；保留原候选报告，最终新fix01报告记录这两项与原四项，再自然结束交回。已有计划隔离、撤权读取、本人历史、未读与时间修复保留，不开启新功能。Lead仍未接受全量UI。
