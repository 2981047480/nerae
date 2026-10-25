# D01-03 fix06 · 最新消息引用应允许明确采用

fix05第11轮正常11分38秒交回，Lead核对60源码/22证据/2报告后原样冻结 `8e5251943ce06ec85f98fcc99769bf019a845f30`。独立类型/构建/102测试通过，23接缝21过2败，失败正是本卡的最新引用可采用/已采用状态；其余21保持通过。现正式派发本卡。继续同会话/OpenCode Go/opencode-go/deepseek-v4.1-flash/Medium、原树/4187，不改Git HEAD；4186/4188/4189/4190均为保留预览，不要碰。

## 实际复现

固定559构建4189，Lead仅用真实UI：重置→教练待办查看林澈→接续草稿/编辑备注→保存8/刷新→取消发布保留旧7→确认交付8（现用6）→最后切周岚并进入其消息→切学员林澈→顾言消息→旧7/刷新/返回（此段通过）→最新8引用。

最新8正文与备注正确，状态“版本8·等待你的决定”，但“采用这份安排”disabled。冷刷新仍disabled，无法进入确认/取消，更无法完成主路径。证据：

- `docs/evidence/D01/round-03/lead/expansion/frozen-04/reference-flow/latest-eight-reference-blocked.json`
- 同目录 `f03-latest-reference-adoption-blocked.txt`与截图
- 教练保存/取消/发布、旧7冷恢复/返回的真实证据也在该目录。

## 根因与修复边界

openMessageRef(plan)总把version传给goToIncomingPlan；incomingView只要historyVersion非null就造available=false/status=new的快照。最新且属于学员本人、可采用的交付也被这样处理；采用之后再看同一最新引用还可能错误显示new。

必须区分最新可采用、历史、教练只读、缺失：
- 学员自己的**点击的最新版本**满足真实版本/hash/关系/可用性时，可进入明确采用确认；取消保留6，确认才更新未来安排，今天3组和历史不变。
- 最新已采用引用反映实际已采用状态，不重新变为等待决定，不重复创建未来安排。
- 旧7在新8后仍是不可变历史，不能借旧引用采用8；教练及他学员只读；缺失不fallback，撤权/关系规则保留。
- 保留fix04迁移/记录返回、fix05动效/恢复/对比/文案。不开新页面、不改品牌/依赖/预览日期、不修改Git HEAD。

Lead独立helper已在8e525运行，共23；结果在 `lead/expansion/frozen-06/code/identity-seams.json`。执行者新增有效正反例与针对性390真实消息→采用取消/确认/已采用再打开；不用重跑全矩阵。新fix06 md/json及三SHA清单，检查/日志如实交回后停止源码。Lead继续最终全量验收，不以替换入口绕过失败。
