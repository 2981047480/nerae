# 交互图（Archify 生成）

五张单文件、可离线打开、可交互（缩放/搜索/聚焦/主题切换/导出）的 HTML 图，源文件与产物都在本目录：

| 文件 | 内容 | 质量档位 | 大小 |
|---|---|---|---|
| [nerae-erd.html](nerae-erd.html) | Nerae 核心数据模型（15 表节选）：账号档案 / 服务交易 / 计划内容 / 训练与运营 | standard | 912 KB |
| [nerae-ia.html](nerae-ia.html) | Nerae 信息架构（学员端 / 教练端 / 运营后台，37 节点） | showcase | 866 KB |
| [nerae-architecture.html](nerae-architecture.html) | Nerae 组件架构（MVP · A 方案）：小程序/后台 → Nginx → Go API/模块群/AI 网关 → MySQL/Redis/COS | showcase | 848 KB |
| [archify-architecture.html](archify-architecture.html) | Archify 组件架构（v3.2.0）：Agent 侧 → IR → 校验/渲染 → 门禁 → 交付 | showcase | 835 KB |
| [archify-features.html](archify-features.html) | Archify 功能地图（30 节点）：生成 / 校验 / 查看 / 交付 / 集成 / 工程 | showcase | 860 KB |
| `nerae-*.json` / `archify-*.json` | 可编辑的 typed JSON 源（改完重跑 `finalize` 即可） | — | — |
| `*.delivery.json` | Archify 交付回执（artifact sha256、门禁状态） | — | — |
| `preview/*.png` | 浏览器渲染预览（light / dark，2048×1320） | — | — |

## 怎么重新生成

需要 Node ≥ 18，以及 Chrome / Chromium / Edge（`finalize` 的真实浏览器门禁必需）。本次使用 Playwright 缓存的 Chrome for Testing，并因沙箱环境设置了 `ARCHIFY_CHROME_NO_SANDBOX=1`：

```bash
export ARCHIFY_CHROME="/path/to/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"
export ARCHIFY_CHROME_NO_SANDBOX=1          # 仅在 Chrome 自身沙箱无法初始化时需要
export ARCHIFY_UPDATE_CHECK_DISABLED=1      # 可选：内网/合规环境关闭更新检查

# 需要先安装 Skill（v3.2.0 为本轮 pin 的版本）
npx skills add tt-a1i/archify -g
SKILL="$HOME/.agents/skills/archify"

node "$SKILL/bin/archify.mjs" finalize erd  docs/diagrams/nerae-erd.json docs/diagrams/nerae-erd.html --quality standard --json
node "$SKILL/bin/archify.mjs" finalize tree docs/diagrams/nerae-ia.json  docs/diagrams/nerae-ia.html  --quality showcase --json
node "$SKILL/bin/archify.mjs" finalize architecture docs/diagrams/nerae-architecture.json docs/diagrams/nerae-architecture.html --quality showcase --json
node "$SKILL/bin/archify.mjs" finalize architecture docs/diagrams/archify-architecture.json docs/diagrams/archify-architecture.html --quality showcase --json
node "$SKILL/bin/archify.mjs" finalize tree docs/diagrams/archify-features.json docs/diagrams/archify-features.html --quality showcase --json
```

只校验不重新交付：

```bash
node "$SKILL/bin/archify.mjs" validate erd  docs/diagrams/nerae-erd.json --quality standard --json
node "$SKILL/bin/archify.mjs" validate tree docs/diagrams/nerae-ia.json  --quality showcase --json
node "$SKILL/bin/archify.mjs" validate architecture docs/diagrams/nerae-architecture.json --quality showcase --json
node "$SKILL/bin/archify.mjs" validate architecture docs/diagrams/archify-architecture.json --quality showcase --json
node "$SKILL/bin/archify.mjs" validate tree docs/diagrams/archify-features.json --quality showcase --json
```

## 口径与已知限制

- **ERD 是节选**：15 张核心表，完整 26 表见 [PRD §7.1](../PRD.md)。为控制密度用了 `standard` 档（showcase 对密集 ERD 过严）；图例已隐藏，键/关系含义写在图下方卡片里。
- **信息架构是整理稿**：按 PRD §8.1 与 §8.2 归类，树表示"包含"而非页面跳转；后台只列审核/风控相关页。
- **Nerae 组件架构**按 [docs/系统架构与技术选型.md](../系统架构与技术选型.md) v1.1 的逻辑架构绘制（组件粒度，不含部署拓扑）。
- Archify 的两张图是基于本次调研（[docs/Archify-调研.md](../Archify-调研.md)）画的理解图，不是 Archify 官方架构图。
- 五张图都通过了 `validate → deliver → check → browser-check` 全部门禁（2026-10-22，Archify v3.2.0），但**未做人工视觉复核**（`visualReview: not-requested`）；预览图来自 Archify `visual-check` 自动截图。
- 生成后 `*.delivery.json` 是交付回执，不要手改；`archify check <html> --require-provenance` 依赖它。
