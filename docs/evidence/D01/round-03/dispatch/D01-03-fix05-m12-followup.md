# 同轮M12/C03补查 · 重试按钮没有恢复状态

原fix05继续，不停止、不重开、不换模型。不新增产品功能：补齐原C03/M12已要求的“同步重试不增组/恢复状态改变”。M04与M05已由Lead在固定559实际复验通过，不要重做。

固定559构建4189、390×900、Full Motion，真实预览选择训练→同步失败（此状态会重置为原3组，Lead已明确区分），关闭预览后按“重试保存”。当时浏览器存储可用，先前实际完成组、暂停、reload均成功。按键后即时和后续终态仍显示“已记录，但还没有保存到本机”及“重试保存”，没有可见恢复。保留3组，没有新增，这是通过部分。

证据（均已落盘）：
- `docs/evidence/D01/round-03/lead/expansion/frozen-04/motion/m12-full-save-failure-before.txt`
- 同目录 `m12-full-save-retried-no-new-set.txt`（即时观测，不当终态）
- 同目录 `m12-full-retry-still-failed.txt`（后续仍失败）

源码TrainingView.vue中syncFailed由preview.training===sync-failed或!storageState.ok决定，按钮只调用retryPersist()；retryPersist仅persistNow，没有成功后解除该预览失败标志，sync-pending同理。请核实并最小收束：实际保存成功才恢复可见状态；实际存储仍失败必须保留失败/输入/原组；重复重试不添加组、不过度弹层，不假造网络同步。考虑普通/休息/完成分支一致。

在同份fix05报告写明此补查。针对性正常/减少动态效果的真实按钮检查与一个真正存储失败的模块反例即可，全矩阵仍交Lead。不要把失败环境当保存成功，不改既有数据/时间线/视觉风格。
