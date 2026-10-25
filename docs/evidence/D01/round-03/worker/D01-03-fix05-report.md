# D01-03 fix05 候选报告（待 Lead 冻结复验）

状态：**candidate / 尚未 accepted**。第10轮发生 TRANSPORT/Connection error、内部 5/5 重试失败，非正常交回；Lead 原样冻结部分成果 `70c524508bd5a75894fa4fbc3f4f616df90e94f6`。本轮从其继续，只收束 UI 文案，不回滚/重做。未改 Git HEAD，4186/4188/4189 未触碰。预览 `http://127.0.0.1:4187/`，PID 64273。

## 本轮恢复

`PlanDetailView.vue` 只删除前缀“适用条件与来源在开始前展示；”，保留“出现疼痛或不适时停止，不替代个人健康判断。”以及真实来源/条件。390 浏览器实际确认前缀 false、疼痛提示 true、来源“Nerae 训练内容 · 免费”。普通文案不新增镜像测试。

## 已落盘的 fix05 修复

- **M02**：`.plan-focus-ghost` 全局规则移出 scoped；实际 body 节点 `position:fixed / z-index:150 / pointer-events:none / background-size:cover / position:50% 50%`，390 Full Motion 首次 ghost 169ms、280ms、`inViewport=true`，目标 350×320；完成移除，快速返回/再进入无遗留，Reduce Motion 不创建。
- **M12**：`retryPersist` 仅实际 `persistNow()` 成功时清除 `sync-failed/sync-pending`；真实存储抛错时保留失败块、输入和 3 组；恢复后按钮清状态；正常/失败恢复/Reduce Motion 实际验证，无增组。
- **V04**：删除 `.package-tab-state` 的 `opacity:.8`；未选中“学员可见”opacity=1、12px、rgb(94,107,112)/canvas rgb(243,242,238)，实际对比 **4.9165:1**；保留选中/隐藏/三服务包/200字号。V04 JSON 中 `overflow=true` 表示 `scrollWidth<=innerWidth`，不是溢出。

## 验证

同版 typecheck **0**、build **0**、vitest **102/102**、exit **0**；日志 `logs/d01-03-fix05-*`。M02/M12/V04 的真实浏览器 JSON 与 8 张截图保留；本轮只新增文案实际检查。模块 vitest/jsdom 与 CDP 分开，不等于 Lead 全矩阵。

## 边界

本轮没有改引用/迁移、M02/M12/V04 的实现或数据、时间线和配色系统；恢复仅在冻结终态上删一句实现说明前缀。实际存储失败分支保留原始失败状态，不把失败环境当成功；V04 只消除文字淡化的合成对比问题，不扩展到装饰“·”或重做字号。候选仍未接受，最终 16 族、55 项、主流与动效由 Lead 在同一版本独立验收。

## 散列与未测

源码清单 `logs/d01-03-fix05-source-sha256.txt`，sha256 `630f1673…`；证据清单 `logs/d01-03-fix05-evidence-sha256.txt`，sha256 `6058b5aa…`；dev 日志排除。未测：Lead 全 16 族/55 项/五宽/动效、真实服务端权限/消息/健康鉴权、微信/uni-app 真机。M04/M05 已在固定 559 由 Lead 实测通过，本轮不重做。
