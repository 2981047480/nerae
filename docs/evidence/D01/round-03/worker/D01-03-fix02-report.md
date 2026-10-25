# D01-03 fix02 候选报告（待 Lead 冻结复验）

状态：**candidate / 尚未 accepted**。第 4 轮约 579 步因 TRANSPORT / Connection error、5/5 重试失败，不是正常交回；Lead 将当时源码原样冻结为 `aaf1fe0a8e9506b72c56157139eeda100d71a709`。本轮在其上继续，未回滚 6a、未改 Git HEAD；55 项/16 族映射沿用 [implementation-map.json](implementation-map.json)，原 D01-03 与 fix01 报告保留。预览 `http://127.0.0.1:4187/`，PID 64273。

## 处理

- **EA21**：计划引用按所属 `studentId` 读取 active/snapshot 的不可变 delivery；林澈 v7 在林澈 v8、教练最后切周岚后仍打开 v7，不被覆盖。教练查看周岚交付为只读“交付版本”，差异对周岚现用 v3，不混林澈现用版。缺失引用进入“引用不可用”，不 fallback 最新；训练引用写入原记录并展开，缺失不展开默认 10/22；服务包按 ID 找所属教练/包。
- **EA22**：同包新 759/v3 显示“尚未确认”，旧 699/4周/v2 单独保留；不再只凭 packageId 判已确认。
- **EA23**：empty/single/insufficient 共用 `recordViewRows`；信息不足时图表/摘要/明细/点评一致为空，单条仅一点且 delta null。
- **EA24**：教练空结果计数与列表为 0，保留已选筛选；加载/错误计数不伪装为 3。
- **EA25**：B 训练日 40 分钟、4 动作；高位下拉器/哑铃按动作；Today 时长、动作数、器械来自会话，非 4 动作内容用 facts 数量，训练图随器械。
- **EA26**：AI 候选只说明改草稿与保存/安排；公开审阅是另一选择。
- **EA27**：add/remove/undo/update 全部同步计划草稿状态与 `todo-draft`；发布/保存/撤销回原内容按真实 dirty 完成，失败不完成，回复/授权不自动完成；发布后显示“进展”。
- **VR09**：消息/我的内容/饮食/记录完整时间统一本地格式；**VR10** 撤权主语改为“教练不能替你恢复”；**VR11** 320/200 侧栏不横向溢出、S03 动作名/组次叠排且图片不叠字；**VR12** 缺图提示左上、不压日期，替代文本保留。另删 EA06“没有虚假等待”。

## 验证

最新同版：typecheck **0**、build **0**、vitest **91/91**、exit **0**；日志 `logs/d01-03-fix02-*`。390×900 实际浏览器复核旧 v7、周岚只读交付、缺失引用、训练原记录、759/699、记录空/单/不足、教练空、B 训练、撤权文案；320×900/200 检查 scrollWidth 与 S03 叠排；强制 404 检查缺图提示。模块为 vitest/jsdom，浏览器为 CDP，两者区分；以上不等于 Lead 冻结版验收。

## 散列与边界

源码清单 `logs/d01-03-fix02-source-sha256.txt`，sha256 `8f18586c…`；证据清单 `logs/d01-03-fix02-evidence-sha256.txt`，sha256 `4f388c32…`；持续 dev 日志排除。旧 `raw.draft` 无可信 owner 时仍只保留 `unassignedLegacyDraft`，两方都不显示，本轮不要求归属恢复 UI。未测：Lead 全 16 族/五宽/200 全屏/动效、真实服务端权限/消息/健康鉴权、微信/uni-app 真机、真实 AI/支付。完整目标仍 active，请 Lead 冻结后独立复验。
