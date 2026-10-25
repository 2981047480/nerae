# D01-03 fix01 候选报告（待 Lead 冻结复验）

状态：**candidate / 尚未 accepted**。基线为 Lead 冻结 `6dba9b6085c9a7f0b2ac84cf0fa8f290d4a7871d`；fix01 改动仍在工作树，HEAD 未变，未 reset 到 b394。55 项/16 族映射沿用 [implementation-map.json](implementation-map.json)，原 `D01-03-report.md/json` 保留。服务仍为 `http://127.0.0.1:4187/`，PID 64273。

## 处理

- **EA17**：回复按 `studentId` 复用/创建会话；周岚有独立 `conv-zhou-lan`/`rel-zhou-lan`。旧合法状态缺关联时，按 active 学员补关系与会话；回复可打开周岚。未知旧约定不借别人的包/价格，标 `needsReview`、`priceKnown=false`。
- **EA18**：健康授权只从 relation 派生，删除 students 副本；教练列表、详情、头部、最近事实、未完成工作、待办、证据、消息正文/摘要/训练引用统一裁剪。撤权不再清空共享消息正文，学生本人历史保留；private 观察即使授权也不给教练；结束关系同样约束。
- **EA19**：去掉共享 `unread`，改为双方 `readBy` 游标；只推进当前方；失败/发送中不投给接收方、不计数；同 ID 成功重试只计一次；列表/线程/摘要/引用/空态同源；重试/删除仅作者。
- **EA20**：`conversation.drafts.student/coach` 隔离；编辑、切换、刷新、发送只清当前方。旧 `raw.draft` 无可靠 owner 时进 `unassignedLegacyDraft`，两方 composer/list/摘要均不显示；旧 `draftOwnerInferred` 也退回未归属，原文保留。新草稿不丢。
- **VR09**：消息/列表统一 `formatTime/formatDateTime`；UTC 07:38 显示本地 15:38，不改存储时间戳。

## 验证

最新同版：typecheck **0**、build **0**、vitest **83/83**、exit **0**；日志 `logs/d01-03-fix01-*`。实际浏览器 390×900/DPR1：周岚回复、旧状态周岚回复、教练未读、失败消息接收面、双方草稿、撤权学生/教练读取面、时间；证据 `logs/fix01-browser*.json`、`screenshots/fix01-390-*.png`（12 张）。模块测试为 vitest/jsdom；以上为 390×900 CDP 实际浏览器检查，但不等于 Lead 冻结版验收。

## 散列与未测

源码清单 `logs/d01-03-fix01-source-sha256.txt`，sha256 `e4855979…`；证据清单 `logs/d01-03-fix01-evidence-sha256.txt`，sha256 `c321721f…`。持续写入的 dev 日志已排除。未测：Lead 10 项接缝与全 16 族状态、五宽度/200%/动效、真实服务端鉴权/消息/健康权限、微信/uni-app/真机、真实 AI/支付；旧草稿无可视归属恢复界面（本轮明确边界，仅保留原文）。

完整目标仍 active；请 Lead 冻结新提交并独立复验。
