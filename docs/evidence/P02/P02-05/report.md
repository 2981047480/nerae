# P02-05 · iOS 设备密钥 UTS 桥

## 状态

**partial**。正式 UTS 插件已实现。HBuilderX 5.33 对正式插件源码完成 iPhoneOS arm64 编译，Swift 源一致性检查与小程序资源构建通过。App 原生资源构建因本机缺少项目锁定 CLI 所需的 `@dcloudio/uni-uts-v1` 包未完成；项目依赖锁定版本与本机 iOS 编译工具也不匹配。没有签名安装、模拟器或真机操作。

## 实现范围

- `apps/client/src/uni_modules/nerae-device-key/` 仅导出创建新设备密钥、读取既有别名公钥、对指定用途签名三项操作。
- iOS 桥调用 `NeraeDeviceKeyStore`；Swift 错误转换成固定错误码，UTS 层拒绝未知错误码或格式异常。原生错误详情、私钥和底层异常不输出；登录证明只作为返回值交给调用方。
- 读取和签名路径只查找既有别名；缺失、别名错误、重复键、密钥类型不符和 Secure Enclave 不可用均返回失败，不会创建替代密钥。只有显式 `createNewDeviceKey()` 可以创建。
- Android、H5 和小程序 fallback 返回 `unsupported_platform`，不使用软件密钥。账号别名映射、登录界面与服务端调用仍未接入。
- 模块内 `DeviceKey.swift`、`DeviceProof.swift`、`SignatureEncoding.swift` 与 `infra/app/ios/device/` 对应文件逐字节一致；`verify.sh` 每次编译前执行 `cmp`，源文件变化会失败。

## 验证证据

- `NERAE_HBUILDERX_APP=/private/tmp/nerae-hbuilder-download/HBuilderX.app docs/evidence/P02/P02-05/verify.sh`：退出码 0。脚本确认三个 Swift 文件一致，并用 HBuilderX **5.33.2026102401**、Node **v26.0.0**、iPhoneOS SDK **26.5** 对正式模块 `app-ios/index.uts` 编译。产物为 unsigned arm64 动态 framework，不是 App。产物 SHA-256：`799c441aaa016e66ca80031237f9c450b53776c1a8da2145a7980e4bf5f2ca20`。编译临时目录在成功后由脚本清理。
- App 与小程序构建使用 `/private/tmp/nerae-p02-05-resource-build.elK1svTa` 中的 HEAD 归档副本，并临时导入插件以确保插件被打包；源 worktree 未修改。目标与复用依赖工作树 `pnpm-lock.yaml` SHA-256 均为 `c18363684e7e3a9f99e2afe73973eaf600703fe13323712acd8cb4a68f028165`。
- `/Users/zephyr/.codex/worktrees/nerae-foundation-integration/nerae/apps/client/node_modules/.bin/uni build -p mp-weixin`：退出码 0，CLI 报 **5.26（vue3）**。非 iOS fallback 已输出到 `dist/build/mp-weixin/uni_modules/nerae-device-key/utssdk/index.js`，内容包含 `unsupported_platform`。`app.js` SHA-256：`39c13a6ba013522cd014a35db8380bbf119870ce94cd92db2abdc7a7008c585d`；插件 fallback SHA-256：`1e73da7907a36feb463c4f9f2ae9d1290d0e574165bb5cd6e35f5fff137a4b29`。
- `uni build -p app`：退出码 1；CLI 报 `Cannot find module: @dcloudio/uni-uts-v1`。临时归档上的 `pnpm install --offline --frozen-lockfile --store-dir /Users/zephyr/Library/pnpm/store/v11` 退出码 1，缓存缺 `@dcloudio/uni-app` tarball；未联网下载，也未改依赖锁。

## 限制与风险

- HBuilderX 5.33 的插件编译结果不能证明与项目依赖的 5.26 工具链兼容；5.26 App 原生资源构建因缺依赖未验证。
- 编译只验证桥接代码能生成 iPhoneOS arm64 framework，没有调用任何创建/读取/签名操作。Secure Enclave 能力、Keychain 行为及错误分类还需真实 iPhone 验证。
- 没有测试签名安装、正式包、登录 HTTP、账号别名隔离或别名持久化；本模块不提供这些能力。

## Lead 复核

复核 UTS 导出、Swift 桥与三份原生源：创建只在显式新设备入口，读取与签名只查既有别名；桥只返回固定错误码，不输出私钥或原生异常。未发现阻止保存该未接线模块的 P0–P2 问题，但编译不是设备安全验收。

Lead 使用同一 `verify.sh` 首次在受限沙箱编译退出 1，Xcode 的共享 DerivedData PIF 缓存无法访问，诊断保留在 `/private/tmp/nerae-p02-05-uts.K3qHJx4O`。随后在获授权的本机 Xcode 构建环境重跑退出 0，得到 unsigned iPhoneOS arm64 framework；该次哈希 `1bfd5a26684050b66232882ce44ab505f02f6ea438926d46c9052106fa810189`，不同构建哈希不视作可复现构建证明。未调用 Secure Enclave、未安装。
