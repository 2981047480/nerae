同一D01-03继续，保持原模型/树。Lead在真实4187又完成S16保存→学员端对照，以下是原S16一致性/无效值验收。证据绝对目录：
/Users/zephyr/Documents/项目/nerae/docs/evidence/D01/round-03/lead/expansion/candidate-02/
services-{saved-new-price,student-preview-new-price,saved-scope-response,actual-student-detail-after-save,negative-price-input,negative-price-restored-without-save}.txt。

EA13 服务字段没有同源：月度包改价759、周期8周、范围「每周2次训练安排；每次训练后的文字反馈」、目标「基础力量与节奏恢复」、响应「工作日12小时内回复」后保存。编辑器与本页预览是新字段，但实际学员教练详情仍显示旧范围每周3次/每4周1调整、旧目标、旧24小时回复，只有价/周期更新。saveServices只更新pack.summary/price/cycle/visible/version，没有同步scope/goals/response等实际公开字段。请将已保存的同包全部公开字段统一到真实学员列表/详情/咨询上下文及预览；旧约定的已确认快照保持原样，不跟随新报价。源码还把所有包一起增版，请只让确有内容变化的包递增版本，避免无关包被更新。

EA14 无效价格被静默变免费：实际价格输入-12，框里立即变0，保存按钮可用、没有就近错误。源updateServiceField用clamp/round/Number||0吞掉负数/空值/非法值。请保留用户正在修正的输入，按允许范围/精度明确校验，错误时禁止保存并就近说明；不能把输入错误当成免费意图。有效0元仍可在明确0时使用。Lead没有保存这个0，已恢复之前759且保存按钮再次disabled；全部仅本地样例，无真实报价/交易。

VR07 同页三个具体细节：
- 「学员看到的内容」预览写¥759 8周，实际详情却写¥759/月、8周，预览丢失计价单位。编辑/预览/详情要明确区分计价单位与服务周期，免费/次/月同源。
- 最后保存用savedAt.slice截UTC，实际本地14:20保存却显示06:20且不标时区；用项目既有日期格式工具正确转换，不改存储时间戳。
- switch名「隐藏服务包」却aria-checked=true代表当前学员可见，名称与状态反义；用稳定状态名称如「学员可见」配checked，或普通操作按钮，不随状态翻转开关语义。
此外「价格与服务范围一起说明」仍是布局解释，和VR06同类请移出产品。

请合并所有已发EA/VR，不重做风格。完成全量报告/55映射与必要验证后交回，Lead再冻结源码独立验收；当前仍未accepted。
