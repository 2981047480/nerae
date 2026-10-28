# F01-03 · 客户端与后台工程壳

状态：partial；已建立最小工程壳并在临时完整工作区验证，待 Lead 将共享 pnpm 锁和任务变更合入同一提交、独立复核及重新验收。执行：`/root/foundation_review`（gpt-6-luna / medium）建立初稿；Lead（gpt-6.1-terra / max）统一依赖、修复 ESM 插件加载、补 H5 入口和复跑。真实任务基线为 `c885515b58961e8738df2d63d5d6387d88e4b8eb`，任务范围增加独立 uni 页面、环境声明与 H5 入口由 Lead 在任务图登记。

## 实际验证与修正

子代理最初的三端临时构建副本遗留了 F01 旧探针的 `vite.config.js` 和 `index.html`，且副本 manifest 没有 `type: module`；这些结果不能证明新工程壳可构建。Lead 在 `/private/tmp/nerae-f01-root-lock-stage` 将实际 client/admin 文件与真实共享锁合并后，先后复现 `uni is not a function`、`Could not resolve entry module "index.html"`。已修正 ESM 下 DCloud CommonJS 插件的默认导出互操作，新增 H5 入口，并移除小程序 manifest 中不必要的 `urlCheck: false`。没有把失败变成成功报告。

2026-10-28 使用 Node 26.0.0、pnpm 11.5.3，按完整 app manifest 生成根 `pnpm-lock.yaml`；解析时供应链策略检查通过，`pnpm install --frozen-lockfile --ignore-scripts` 退出 0；有一次 TypeScript tarball 网络重试后完成。`pnpm peers check` 退出 0，无 peer 冲突。依赖安装提示 `vue-i18n@9.14.5` 和 `phin` 弃用；尚未进行依赖安全审计。

| 命令 | 结果 |
|---|---|
| `pnpm --filter @nerae/client typecheck` | 退出 0，TypeScript 5.9.3、vue-tsc 3.3.13 严格检查实际 SFC 与配置 |
| `pnpm --filter @nerae/admin typecheck` | 退出 0，TypeScript 严格检查 |
| `pnpm --filter @nerae/client build:h5` | 退出 0，`dist/build/h5/index.html` 存在且非空 |
| `pnpm --filter @nerae/client build:mp-weixin` | 退出 0，`dist/build/mp-weixin/app.js` 存在且非空 |
| `pnpm --filter @nerae/client build:app` | 退出 0，`dist/build/app/app-service.js` 存在且非空 |
| `pnpm --filter @nerae/admin build` | 退出 0，`dist/index.html` 存在且非空 |

## 边界

三端结果均是 uni-app 资源编译，不是 iOS/Android 原生安装包、签名、真机或微信开发者工具验收。当前入口为空工程壳，无伪登录、业务数据、正式 UI 或训练/休息计时；业务功能与第五轮 UI 在后续任务实现。实际小程序 AppID、原生平台账号与签名均尚未配置。临时工作区的通过结果不能代替集成后的干净检出复跑。
