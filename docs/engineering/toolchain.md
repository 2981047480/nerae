# 工具链基线与探针

2026-10-28，F01-01 进行中。根目录的 `.node-version`、`.go-version`、`package.json`、`pnpm-workspace.yaml` 和 `pnpm-lock.yaml` 固定本轮探针使用的版本及依赖；正式应用目录由后续任务建立。

| 工具 | 本轮验证版本 | 说明 |
|---|---|---|
| Node | 26.0.0 | 本机 macOS arm64；仅验证下述资源编译 |
| pnpm | 11.5.3 | 根锁文件格式由此版本生成 |
| Go | 1.27.3 | 临时解压版本通过编译测试；尚未安装为本机长期工具 |
| uni-app Vue 3 | 3.0.0-5020620260917001 | 基于 DCloud 官方 Vue 3 模板候选版本；Vue 3.4.21、`@dcloudio/types` 3.4.31、Vite 5.2.8 |

实际探针源码和当时锁文件保存在 `docs/evidence/F01/F01-01/probe/`，仅供复现编译，不是业务应用。将该目录复制到独立临时目录，在具备上述 Node/pnpm 版本和 npm 网络访问时，执行：

```sh
pnpm install --frozen-lockfile --ignore-scripts
pnpm peers check
pnpm run build:h5
pnpm run build:mp-weixin
pnpm run build:app
```

根目录使用 `pnpm install --frozen-lockfile --ignore-scripts` 安装锁定依赖；本轮也以已填充的临时 pnpm store 执行了 `--offline` 复装。安装依赖需要合适的网络和包管理器，不应把本机临时 store 当成项目资产。

Go 官方 `go1.27.3.darwin-arm64.tar.gz` 的 SHA-256 为 `dd3908cf20959d798c1e06ebe5b72213f0505ce01373b40ce3110975a6cd99c2`；本轮校验后解压至 `/private/tmp/nerae-go-toolchain-1.27.3`，以 `docs/evidence/F01/F01-01/go-probe/` 的原始源码在独立临时目录重跑 `go test ./...`。临时目录会被清理；其他开发环境需从 Go 官方渠道获取并校验适合其系统的发行包。

注意：探针只生成 H5、小程序和 App 资源，不验证微信开发者工具、iOS/Android 签名、安装包、真机、正式应用或业务行为。安装时 `vue-i18n@9.14.5` 及传递依赖 `phin` 出现弃用警告，生产使用前需核验维护与安全风险并验证兼容升级；本轮未完成依赖安全审计。正式工程及干净检出复现、任务接受提交和复核仍待后续完成。
