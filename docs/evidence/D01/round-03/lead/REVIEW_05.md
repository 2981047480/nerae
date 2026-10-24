# D01-03 首次冻结候选审阅：不接受

源码冻结：`6dba9b6085c9a7f0b2ac84cf0fa8f290d4a7871d`，树`/private/tmp/nerae-ui-complete`。原执行轮在第1轮336步发生TRANSPORT Connection error，5/5重试后失败，未生成完整报告。Lead原样冻结已保存产物，没有编辑UI；同会话/同模型第2轮已正常补交回文档，未改UI；第3轮fix01正在修复下述四项。此为候选，不是接受、集成或发布。

## 已执行

- Lead独立类型检查、Vite构建、75个测试均exit0。检查前后60份prototype文件SHA一致，见`expansion/frozen-01/code-checks.json`及原始日志。
- 独立实际store模块、替身window/storage的6项身份接缝检查：3通过、3失败，exit1，前后源码一致。通过的是周岚实际备注编辑/保存切换不改林澈、林澈采用不改周岚、周岚交付不改林澈消息与incoming。
- 首次检查脚本在两个通过用例中误用了`updatePlanField`参数，未实际产生预期编辑；已校正为dayId/exerciseId/note四参并断言编辑生效，重跑结果仍为3通过/3失败。`identity-seams-v2.log`和`identity-seams-summary.json`为当前结论，保留原日志，不把原设置当成功证据。
- 实际4187/CUA/390×900/DPR1复现以下失败，截图前后尺寸与滚动一致，未裁图或改写DOM；流程截图使用保留当前视口的方法，不调用viewport.set重置真实滚动。
- 合法旧保存状态冷重开可恢复759元/月、8周、新范围/目标/12小时回复；旧约定仍699元/月、4周、原名称/确认时间。新控制台warn/error0。恢复证据保存后通过界面重置可丢弃样例，开始独立路径测试。

## 阻止接受的缺陷

| ID | 实际结果与影响 | 证据 |
|---|---|---|
| EA17 | 周岚显示服务进行中，点击回复仍留在详情，提示先确认关系；未打开/创建属于周岚的对话。已有关系不能误用林澈会话，也不能因缺健康授权阻断普通消息。 | `frozen-01/zhou-before-reply.txt`、`zhou-after-reply.txt`、`screenshots/zhou-reply-no-conversation.jpg` |
| EA18 | 学员在消息中真实撤权后，切教练列表/详情仍显示林澈已授权及4项健康证据，包括训练重量、饮食观察（且写仅本人可见）。教练消息仍有既有训练反馈与12组引用；学生自己的健康消息反而被清空为不可见。授权副本与消息health标签未统一，不能仅修种子中的周岚。 | `health-revoked-student-thread.txt`、`health-revoked-coach-detail.txt`、`revoked-coach-thread.txt`、`revoked-coach-raw-dom.json`及同名截图 |
| EA19 | 学员发送本地普通安排消息成功，切教练消息列表已有新正文，但Gu/Lin对话未读仍0；打开会话把单个共享unread清0，未区分双方读取位置。 | `ordinary-student-send-after-revoke.txt`、`coach-unread-after-student-send.txt`、`screenshots/coach-unread-missing.jpg`、身份检查结果 |
| VR09 | 新消息在当地15:38左右发送，却显示07:38。MessagesView的timeLabel直接slice ISO的11–16位，截取UTC而不是应用时区；不应修改存储时间戳解决。 | 上述普通消息/教练列表DOM；冻结`MessagesView.vue`的timeLabel |

所有相对证据路径在`docs/evidence/D01/round-03/lead/expansion/`，表内省略的`frozen-01/`与当前冻结检查目录相同。预览角色切换的jumpToScenario只改actor/screen/nav，不恢复健康授权；撤权后教练数据暴露不是预览重新授权造成的。

## 后续与边界

先由指定执行者集中修复四项并保留旧候选报告，再重新冻结、重跑独立接缝检查。完整16族五宽度/100与200文字、M01–M12及其他状态/恢复尚未在此冻结版完成；本次不因75测试绿勾而接受，也不在已知逻辑错误时扩大截图矩阵。

映射结构55项、16族、7流、12动效齐全，但实际UI接受仍pending。模块替身不等于浏览器/后端；本地UI样例不证明真实登录、健康权限、AI、消息服务、微信真机、用户研究或发布。
