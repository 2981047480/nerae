# D01-03 fix01 独立复验 · 未接受

源码冻结：`6a5609c5b0ecdb350be080350237f1a4e86d48b9`，指定 Harness 正常交回后由 Lead 原样提交。实现仍归 OpenCode Go / deepseek-v4.1-flash / Medium；Lead 未改 UI 源码。60 个源码文件在本次检查期间保持干净。

## 已通过的证据

- `expansion/frozen-02/code/summary.json`：独立 typecheck/build/83 测试 exit0；10 项真实 store 模块接缝检查通过（模块检查使用 fake window/storage，不能代替浏览器或后端）。
- 同目录 retained-data-ui-checks.json：旧数据未重置前，周岚回复、双方独立草稿、失败消息接收方不可见、同 ID 重试只出现一次/未读一次、刷新与撤权保持，通过真实 CUA 操作。
- 重置后真实撤权，学员自己的正文保留，教练列表/详情/待办/审阅依据屏蔽健康内容，未重新授权；明确 self-only 观察不因一般授权放行。见 fresh-revocation-* 与 grant-still-excludes-self-only-observation。
- 周岚待办进入本人草稿，发布给周岚后林澈现用6/收到7不变；林澈新交付8时现用6不变。见 zhou-*、lin-v8-delivered-v6-current。
- 多训练日 B 的选择和刷新、开始对象、保留原3组、记录42.5kg×11、真实75秒休息倒计时、暂停/刷新、无效重量提交阻断。见 selected-day-b-*、start-day-b-*、day-b-*、training-negative-submit-blocked、replacement-keeps-original-three-confirmed-sets。
- 匿名开始需要登录，取消保持内容；年龄未知单独确认，未成年阻断开始。见 anonymous-*、unknown-age-*、minor-*。
- 新服务759/完整范围/目标/响应保存回读；旧699约定未改；负价保留且保存禁用。自有内容公开审阅包含全部动作/组数/目标/休息/公开备注，健康备注排除，提交后仍待审核；AI候选应用一次/刷新禁重复；饮食自评回读不造摄入。见 service-*、student-coach-detail-*、share-*、own-content-*、ai-*、diet-self-assessment-*。
- 18 个视图矩阵（16族，学员列表/详情、计划编辑/审阅另列）×5宽×100/200字号，共180捕获（含会话）。main/skip/重复ID/命中区基本通过，S01窄屏200字号发现内部裁切。实际查看各族390/1440普通字号截图，整体构图、配色、分隔、密度与首批基线连续；尚不能最终接受。

## 新发现 / 未通过

详见 `../../dispatch/D01-03-fix02-packet.md`。旧v7消息实际打开v8，v8交付时间仍写死10月23日；同包新759报价误标已确认且隐藏699原约定；记录空/单条时图表和明细不一致；教练无结果状态仍展示3人；换计划后器械/时长沿用旧内容；公开审阅被错误说成私有AI草稿生效的必要条件；已发布周岚仍显示草稿待保存；撤权确认代词错误；部分保存时间仍切UTC；200字号内部裁切及直接实现理由文案。

## 证据使用限制

`states-summary.jsonl` 的早期连续状态扫查仅作发现线索：同一族内前一状态可能改数据，不用于声称各状态独立通过；其中 S05 empty/single 只改预览标志，图表/明细矛盾另有源和DOM互证。`states-isolated-summary.jsonl` 标明 resetBeforeEachState=true 的记录才是逐态隔离检查。中途遇弹层切换关闭/控件语义定位超时均保留真实过程，不算产品通过或失败；后续按实际DOM更正定位。

截图有 before/after CSS宽高、DPR、滚动核对；尺寸变化帧标记 excluded，另取稳定帧，不裁图掩盖问题。本轮未完成全部真实主路径/12动效最终验收，未测系统字号/系统减少动态效果/真机/帧率。全量未导入 main，D01-03/D01-I 未接受，目标仍 active。
