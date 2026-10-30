# P02-05 · iOS 设备密钥 UTS 桥

## 状态

**partial**。正式 UTS 插件已实现。HBuilderX 5.33 对正式插件源码完成 iPhoneOS arm64 编译，Swift 源一致性检查与小程序资源构建通过。App 原生资源构建因本机缺少项目锁定 CLI 所需的 `@dcloudio/uni-uts-v1` 包未完成；项目依赖锁定版本与本机 iOS 编译工具也不匹配。没有签名安装、模拟器或真机操作。

## 实现范围

- `apps/client/src/uni_modules/nerae-device-key/` 导出随机创建、按预留 alias 创建一次、读取既有别名公钥、对指定用途签名四项窄操作。
- iOS 桥调用 `NeraeDeviceKeyStore`；Swift 错误转换成固定错误码，UTS 层拒绝未知错误码或格式异常。原生错误详情、私钥和底层异常不输出；登录证明只作为返回值交给调用方。
- 读取和签名路径只查找既有别名；缺失、别名错误、重复键、密钥类型不符和硬件不可用均返回失败，不会创建替代密钥。创建只能由显式 `createNewDeviceKey()` 或 `createDeviceKeyAtReservedAlias(alias)` 发起。
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

## 2026-10-31 已预留 alias 的 create-once 接口

新增 `createDeviceKeyAtReservedAlias(alias)`，接受 caller 已生成并先行持久化的 43 字符 canonical 无填充 base64url 编码（32 字节）。iOS 和 Android 在各自进程锁内检查 exact tag/alias 并创建 P-256 硬件密钥；已占用时返回固定 `alias_conflict`，不覆盖、不删除。Android 继续执行原 TEE/StrongBox/安全硬件检查，不提供软件密钥回退；验证失败时新方法保留该 exact alias 的条目供只读核对，读取路径仍会拒绝非硬件密钥。iOS 桥 `DeviceKey.swift` 与 `infra/app/ios/device/DeviceKey.swift` 同步。

调用约束：caller 必须在创建前持久化 alias；创建结果不确定时只允许 `readExistingDevicePublicKey(同一 alias)` 核对，不能换 alias 或重试到另一个 alias。挑战可能已发送后只能读取已有密钥，create-once 接口不提供恢复语义。接口不负责生成/保存 alias，也不执行客户端 vault 状态机。

验证命令与结果：

- `NERAE_JAVA_HOME=/private/tmp/nerae-hbuilder-download/HBuilderX.app/Contents/HBuilderX/plugins/amazon-corretto NERAE_KOTLINC=/private/tmp/nerae-kotlin-1.9.24/kotlinc/bin/kotlinc NERAE_ANDROID_JAR="$HOME/Library/Android/sdk/platforms/android-35/android.jar" NERAE_HBUILDERX_APP=/private/tmp/nerae-hbuilder-download/HBuilderX.app NERAE_NODE=/opt/homebrew/bin/node bash docs/evidence/P02/P02-05/verify-reserved-alias.sh`：退出码 0。Swift canonical alias 正反例通过；HBuilderX 5.33 Android UTS 转换产出 wrapper 和 native Kotlin 文件，生成 wrapper 包含新增操作；Kotlin 1.9.24 对正式 Android bridge 编译成功；canonical alias 正反例通过。仅出现既有 `isInsideSecureHardware` deprecated 警告。此验证没有运行 Keystore。
- `NERAE_HBUILDERX_APP=/private/tmp/nerae-hbuilder-download/HBuilderX.app NERAE_NODE=/opt/homebrew/bin/node bash docs/evidence/P02/P02-05/verify.sh`：退出码 0。源码一致检查通过，HBuilderX 5.33 编译出 unsigned iPhoneOS arm64 framework；该结果不证明 Secure Enclave 可用或设备操作成功。
- 最终 unsigned arm64 framework SHA-256：`352596e80d5d0420238e7aa1cb426659952d351cfdb5054eaefdb372094e3ffa`。
- `NERAE_JAVA_HOME=/private/tmp/nerae-hbuilder-download/HBuilderX.app/Contents/HBuilderX/plugins/amazon-corretto NERAE_KOTLINC=/private/tmp/nerae-kotlin-1.9.24/kotlinc/bin/kotlinc NERAE_ANDROID_JAR="$HOME/Library/Android/sdk/platforms/android-35/android.jar" bash docs/evidence/P02/P02-06/verify-der.sh`：退出码 0。原 Android Kotlin/DER 回归通过；5 种损坏 DER 与篡改签名被拒绝。`bash -n docs/evidence/P02/P02-05/verify-reserved-alias.sh` 与 `git diff --check` 通过。

设备不可用限制：未在 iPhone 或 Android 硬件上验证预留 alias 首次创建、同 alias 并发/重复冲突、Keychain/Keystore 中已存在密钥未被改写、或硬件级别拒绝行为。Swift/Kotlin alias 检查和 UTS 转换不能替代这些真机检查；本任务不构成账号映射持久化或登录恢复实现。

## Lead 复核

复核 UTS 导出、Swift 桥与三份原生源：创建只在显式新设备入口，读取与签名只查既有别名；桥只返回固定错误码，不输出私钥或原生异常。未发现阻止保存该未接线模块的 P0–P2 问题，但编译不是设备安全验收。

Lead 使用同一 `verify.sh` 首次在受限沙箱编译退出 1，Xcode 的共享 DerivedData PIF 缓存无法访问，诊断保留在 `/private/tmp/nerae-p02-05-uts.K3qHJx4O`。随后在获授权的本机 Xcode 构建环境重跑退出 0，得到 unsigned iPhoneOS arm64 framework；该次哈希 `1bfd5a26684050b66232882ce44ab505f02f6ea438926d46c9052106fa810189`，不同构建哈希不视作可复现构建证明。未调用 Secure Enclave、未安装。

## 2026-10-30 HBuilderX 源码根资源编译探针

按 [DCloud 官方项目说明](https://uniapp.dcloud.net.cn/quickstart) 将 `apps/client/src` 复制到独立临时目录并作为 HBuilderX 项目导入，使其使用 HBuilderX 自带编译器，而非项目锁定的 CLI 编译器。仅在临时副本的页面中加入设备密钥插件导入和编译调用；没有运行或安装该夹具。执行 `cli publish app-ios --type appResource --project /private/tmp/nerae-hx-build-probe.dQ4Je0/nerae-hx-src` 退出 0，日志显示 **5.33（vue3）** 编译及本地资源导出成功。实际产物包括 `app-service.js`、转换后的 iOS `index.swift`（SHA-256 `ac8b5a744a17cb3d43a5a8e284a2b0a875c12bb06fe2997e5ed33a5a0b010b4c`）及 Android `index.kt`（SHA-256 `bccb65abf72b1988640035bfdd9e0c7f052c3d04fb2bf678d2c9da712927fd0e`），以及两端桥源码。临时输出路径及哈希只用于本次证据，不是可复现二进制或交付件。

另执行 `cli launch app-ios --iosTarget device --compile true --continue-on-error false`，CLI 退出 0，但输出说明 iPhone 标准基座未签名并停止，未输出可运行 App；`base info` 报标准基座 `isSigned=false`、自定义基座不存在。重查系统代码签名身份有 1 个有效条目，两个常见描述文件目录未找到 `.mobileprovision`。受限沙箱中的 HBuilderX CLI 因 CPU 特性检测退出 134；以上结果来自授权本机环境。未启动模拟器、上传代码/证书、安装或运行 App。

该探针解决了使用 HBuilderX 自带 5.33 编译器导出夹具资源的路径；它没有证明生产工程与 5.33 在行为上兼容，也没有完成 Android Kotlin 运行时链接、iOS 签名基座、原生应用封装、真实设备密钥或业务登录。CLI 5.26 的缺包问题依旧存在，不能直接以此替代锁定工具链的验收。
