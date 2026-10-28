# R04 色彩比较 · 2026-10-27

> 历史试览：用户随后否定本轮颜色及内容呈现，已由 [R05](../../round-05/BRIEF.md) 接续；下面保留当时的实现与检查结果，当前页面不再提供这三个候选。

用户认可当前重做方向，希望配色更有辨识度。Lead 直接完成颜色迭代、实际页面检查与回归；无子代理。本轮只调整颜色角色和预览入口，不重排版、不改训练记录逻辑、不增加正式产品设置。当前仍是本地 Vue 样机。

## 三个候选

| 候选 | 主要颜色 | 设计判断 | 桌面 / 手机证据 |
|---|---|---|---|
| 梅酒与米纸 | 暖纸底 #F0E8DE、梅酒 #753C56 | 推荐试览默认；暖底降低生硬感，深梅色保留清晰行动提示 | [首页](mulberry-today-desktop.jpg) / [记录](mulberry-record-mobile.jpg) |
| 松墨与铜金 | 松墨底 #152724、铜金 #DBB77D | 更沉静、有力量；采用完整深色表面与浅色文字 | [首页](pine-today-desktop.jpg) / [记录](pine-record-mobile.jpg) |
| 雾紫与青柠 | 紫雾底 #EFEDF4、青柠 #D5EB86 | 更轻盈、鲜明；青柠限于按钮底，正文链接仍用可读的深紫色 | [首页](lilac-today-desktop.jpg) / [记录](lilac-record-mobile.jpg) |

用户在「预览 → 色彩方向」选择即可返回原页比较；App 记住本机外观选择。外观存储键独立于训练草稿，三套方案使用同一组 ID 和输入。默认第一套不表示用户已经批准品牌定版。AI 不推测训练事实。

## 代码范围

`src/state/palette.ts` 是三套颜色的单一来源，`main.ts` 在挂载前恢复；颜色使用背景/表面/文字/强调/按钮前景/状态等语义角色。`PreviewControl.vue` 增加可按键操作的选择按钮，`aria-pressed` 标记当前项。基础样式及各页只替换颜色引用，使深色方案覆盖表格、消息和面板。未修改业务 store、API、ER、依赖或锁文件。

## 本次验证

- `/opt/homebrew/bin/node node_modules/vue-tsc/bin/vue-tsc.js --noEmit`：退出 0。
- `/opt/homebrew/bin/node node_modules/vitest/vitest.mjs run --reporter=dot`：退出 0，5 文件 / 131 测试通过，见 [vitest.log](vitest.log)。含既有 jsdom scrollTo 未实现提示，未造成失败。
- `/opt/homebrew/bin/node node_modules/vite/bin/vite.js build`：最终颜色修正后退出 0，见 [build.log](build.log)。131 测试在最后两套的 muted/placeholder 色值微调之前运行，之后仅调整色值，类型检查和构建已重跑。
- `git diff --check`：退出 0。
- 真实 IAB 检查三套桌面首页、三套手机记录；补查深色逐组记录、发现、消息。页面无横向溢出，记录输入与模式按钮边界在手机可视宽度内。切换及刷新后的组 ID/输入相同；配色可恢复，见 [浏览器读数](browser-values.json)。未清空原有样例记录。
- 从浏览器读取计算后的颜色，逐套检查 18 组文字/底色角色；最低分别为 4.62、5.03、4.72，均超过 4.5，见 [contrast.json](contrast.json)。先前发现的浅色小字与占位色已加深。此项是角色配对抽查，不代表逐元素、所有状态或完整无障碍认证。
- 最后读取页面控制台，捕获到的 error 为空；工具自身的 Statsig 警告不计入产品控制台。

截图字节、源码及构建的 SHA256 与尺寸见 [manifest.json](manifest.json)。浏览器读数中 `restored` 是修正前的恢复检查，仅用于核对组 ID/输入；最终颜色依据 `samples` 与 contrast.json。所有截图为实际页面捕获，没有用参考图替代实现。

## 边界

两种记录方式、日期、草稿/错误与权限规则沿用 R04；没有重新加入训练或休息计时器。此轮未运行 HTTP/DB、微信真机或正式业务服务，不变更正式 U 卡接受状态。用户的最终色彩选择仍待反馈；原始 R04 验证证据保留为历史版本。
