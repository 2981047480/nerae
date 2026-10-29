# P02-04：iOS 设备密钥与签名基础

状态：partial，未接受。基线 `cb6a455`；任务执行与技术判断由 Lead 承担，独立审核由现有 reviewer 只读完成。本项仅为原生基础，不是可安装 App，也没有接入 uni-app。

实现使用 iPhoneOS Security 框架申请 Secure Enclave P-256 永久私钥，访问级别为首次解锁后且仅本机；仅导出公钥，生成服务端要求的 ES256 设备证明。创建新身份使用随机别名，使用已有身份时只查找、不自动创建替代密钥；发现缺失、重复或无法验证硬件属性时失败关闭。别名只标识本机密钥，账号到别名的映射和切号隔离尚未实现。不存在软件密钥回退路径。证明在普通字符串描述中脱敏；发送端仍须禁止日志及非 TLS 传输。

验证（本机，非模拟器）：

- Xcode 26.6、iPhoneOS 26.5 SDK；`NERAE_GO_BIN=/private/tmp/nerae-go-toolchain-1.27.3/go/bin/go infra/app/ios/device/verify.sh` 在授权环境退出 0：iOS arm64、iOS 15 编译与链接通过（这里只是编译目标，非最终最低版本）；128 个 macOS 临时软件密钥签名的 DER 转换与 CryptoKit 结果一致，畸形 DER 和证明脱敏检查通过；Swift 生成的合成证明通过仓库 Go 服务端验证器，错误用途、nonce、篡改和过期被拒绝。测试通过临时 Go overlay 注入测试，不写入服务端包或保留测试密钥。
- 沙箱环境执行同一脚本退出 1：iOS 编译和链接通过，但本机 Security 框架生成临时软件密钥返回 `-50`；脚本已报告失败阶段。授权环境复跑完整通过，并不证明真机结果。
- `bash -n infra/app/ios/device/verify.sh` 通过。只读审核未发现当前 ES256 格式与服务端明确不兼容，指出账号映射未实现和测试需区分失败阶段；测试诊断已补齐，账号映射作为集成阻塞保留。

未验证/阻塞：iPhone 上 Secure Enclave 的生成、重启后查找、属性返回和锁屏行为；多进程并发访问、异常中断和孤立密钥清理；同一账号重登、不同账号切换与别名持久化；安装包、签名、匹配 uni-app 编译版本的原生桥、真实网络与服务端互通；设备丢失、恢复码和人工申诉。应用调用方必须在真实账号模型中可靠绑定每个主体与设备别名，不得将一个别名分配给两个主体；首个匿名登录与绑定完成之间的异常恢复需要设计。客户端时钟偏差和系统 Keychain 可用性必须在真实体验中处理。没有这些证据前不得宣称跨设备登录、硬件密钥或 P02-04 验收完成。


## 2026-10-30 UTS 编译探针

在独立临时源码夹具中复制本卡 Swift 源码并加入仅供编译的 UTS/Swift 桥；未安装到手机、未上传、未调用真实设备密钥。HBuilderX 5.33 CLI 起初将含 `package.json` 的外层夹具识别为 CLI 工程并报告缺少 `node_modules`；改成独立源码根后触发官方 iOS 扩展安装。插件管理器显示 `uts-development-ios` 1.6.0.2026092217 已安装，但随后编译、重载扩展、重启 HBuilderX 再编译仍报告“正在安装 uts iOS 运行扩展”，未到实际编译阶段。CLI 在该失败情况下返回 0，必须按输出及产物而非退出码判断。只读检查对应编译器代码显示该提示由未取得 Swift compiler server 触发，根因尚未确认。临时夹具和扩展保持原状供排查。

探针使用 HBuilderX 5.33，项目锁定 uni 编译器 5.26，不能将它视为匹配工具链的成功验证。仍未得到原生桥、iOS 签名安装、真实手机或完整 App 的证据。

后续只读检查 HBuilderX 编译启动代码发现平台扩展路径由启动参数选择，单模块 CLI 路径可能未带入 iOS 扩展；这只是待验证的原因。另建不含业务数据的 Swift/UTS 源码夹具，执行 `cli launch app-ios --iosTarget device --compile true --continue-on-error false`：CLI 退出 0，但仅输出 HBuilderX 版本，未见编译日志或夹具构建产物，不能记为成功。未启动模拟器或安装任何 App。HBuilderX 的 iPhone 设备列表无结果；Xcode 只读设备列表显示 iPhone 已配对、开发者模式已启用，但连接通道为 unavailable，不证明当前设备可用于安装。未继续重复该路径。

## 2026-10-30 官方 iOS UTS 独立编译增量

只读检查定位到 HBuilderX 5.33 自带的 `uts-development-ios` 编译扩展与 `plugins/launcher/base/iPhone_base.ipa`。显式设置插件目录后，官方 `getSwiftCompilerServer()` 能加载并且 `checkEnv()` 返回 `code: 0`；先前 CLI 报扩展不可用的具体根因仍未证实。未使用之前被站点安全拦截的下载来源。

用官方 `uni-uts-v1/dist/swift.js` 的 `runSwiftDev` 在独立临时夹具编译：显式传入 `UNI_INPUT_DIR`、`UNI_OUTPUT_DIR`、`UNI_HBUILDERX_PLUGINS`、`IOS_STANDARD_BASE_PATH`、`IOS_RUNTIME_NASE_PATH`、`HX_Version=5.33`，指定独立 `TMPDIR` 以限定插件自清理范围；先对仅含合成 echo 的 UTS/Swift 夹具进行探针，再对复制的 `DeviceKey.swift`、`DeviceProof.swift`、`SignatureEncoding.swift` 及仅供编译的签名桥验证。`runSwiftDev` 两次返回 `code: 0`，实际生成 `unimoduleNeraeDeviceProbe.framework`；`file` 显示 arm64 Mach-O 动态库，`xcrun vtool -show-build` 显示 platform IOS、最低 iOS 15.0、SDK 26.5。复制源码经 `cmp` 与集成树一致。framework 本身未签名；探针桥的错误字符串仅用于验证编译，不是产品 API，不接入或运行。

直接调用第一次因测试参数将预期 map 误写为数组而失败；修正后进入编译，但未指定官方基座路径导致空参数错误。指定 HBuilderX 自带基座后，沙箱内 Xcode 构建因无法建立默认 DerivedData 缓存失败；授权环境在隔离临时目录重跑成功。没有启动模拟器、安装、上传或执行签名功能。生成产物位于 `/private/tmp/nerae-uts-device.8MnuqDWw/input/unpackage/modules/unimoduleNeraeDeviceProbe.framework`，为临时证据，不是应用包。

受限于 HBuilderX 5.33 与项目锁定 uni 编译器 5.26 不一致，这只证明该版本组合下真实 Swift 源码可通过 UTS 原生编译，尚不证明项目锁定版本兼容，也不证明安全芯片或 iPhone 运行。`security find-identity` 在授权环境报告 1 个有效签名身份，两个标准描述文件目录中未找到 `.mobileprovision` 文件；设备列表在授权环境报告 iPhone 14 Pro Max `available (paired)`，尚未安装。正式桥的身份映射、错误处理、uni-app 接线、签名描述文件、完整 App 和真实登录同步仍待实现。

复现脚本 `infra/app/ios/device/verify-uts-probe.sh` 在授权环境运行 `NERAE_KEEP_PROBE=1 .../verify-uts-probe.sh` 返回 0；用当前仓库源码生成 iPhoneOS arm64 动态 framework，SHA-256 `b7260d43b30bb2b5d6f534b063a35c5fa734eb9ed85e648b60490002dc9192b6`，独立夹具 `/private/tmp/nerae-uts-probe.9VPW5T1T` 被主动保留。脚本默认只清理自己新建且成功的临时目录，失败会保留；编译本身不验证 iPhone、硬件或服务。该 SHA 仅标识本次构建产物，不保证不同构建路径可重现相同字节。
