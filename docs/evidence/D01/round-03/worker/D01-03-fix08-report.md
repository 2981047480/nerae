# D01-03 fix08 候选报告（待 Lead 冻结复验）

状态：**candidate / 尚未 accepted**。基线为 Lead 冻结 `29bddb694e1498549aaa20cbc24511c9d45a515e`；未改 Git HEAD，4186/4188/4189/4190/4191/4192 未触碰。预览 `http://127.0.0.1:4187/`，PID 64273。

## 修复

`applyPreviewState('messages', ...)` 之前对 `state.conversations` 做 filter，凡含 `preview-pending` 的顾言整段会话被删除，切换 normal 后真实历史与草稿丢失。现改为只从各会话的 `messages` 中删除 `preview-pending` 占位；不重建 seed、不删除会话、不改真实消息/双方草稿/其他会话。发送失败/发送中仍可显示本方占位；normal 及非发送态清除占位。

## 验证

模块：failed→normal 后顾言会话仍在、历史 4 条、学员/教练草稿均保留、江遥会话不变；无占位时 normal 亦不改任何真实会话。390 实际：顾言输入真实私稿→预览 failed（5 条，含占位）→normal（4 条，textarea 草稿仍在）→刷新仍保留；其他会话 ID 不变。类型/构建/测试同版 exit 0。

同版 typecheck **0**、build **0**、vitest **113/113**、exit **0**；日志 `logs/d01-03-fix08-*`，3 张截图与 JSON 已保存。保留 fix07 图表/S16、fix06 最新引用/单位、fix05 M02/M12/V04、fix04 迁移/记录全部回归。

未测：Lead 全 16 族/55 项/五宽/动效、真实服务端权限/消息/健康鉴权、微信/uni-app 真机；教练本人发送中/失败的预览需真实教练发送，本轮未扩作者逻辑。候选未 accepted。

## 边界与未解决

恢复逻辑只处理预览占位消息，不改变真实发送、重试、未读游标或私稿归属。含占位与不含占位两种路径都有正反例；别的会话消息数组保持原样。真实发送失败/重试仍由既有消息逻辑与 Lead 走查，本卡不扩展作者/发送状态机。当前无已知范围内代码缺陷，候选仍待 Lead 独立接受。

## 散列

源码清单 `logs/d01-03-fix08-source-sha256.txt`，sha256 `d6371f3c…`；证据清单 `logs/d01-03-fix08-evidence-sha256.txt`，sha256 `e8c67d4b…`；dev 日志排除。请 Lead 冻结后最终复验/集成。
