# P02-07 · iOS 认证状态安全存储

状态：partial，未接受。基线 `0059ed7`；Lead 实现，独立 reviewer 只读检查。仅为原生存储基础，尚未接登录页面、账号映射或服务端，不是可安装 App。

## 实现

- `nerae-secure-vault` 的 iOS 窄接口可读取、替换和清理一个应用私有认证状态对象；只接受 UTF-8 JSON object，限制为 64 KiB。存储内容的业务 schema、版本、设备别名与用户主体绑定必须由后续认证客户端负责，不能把本地状态当作有效会话或授权。
- Keychain 查询固定 service、account 和 `synchronizable=false`，不提供由请求输入任意定位或删除其他项的接口。新项使用 `kSecAttrAccessibleWhenUnlockedThisDeviceOnly`；更新同步设置该属性，读取核对属性。根据 [Apple 官方说明](https://developer.apple.com/documentation/security/ksecattraccessiblewhenunlockedthisdeviceonly)，该属性仅在设备解锁时允许访问且不会迁移到另一台设备。
- 仅 `errSecItemNotFound` 映射到“无状态”，锁屏、权限、损坏和其他错误返回固定不可用；写入失败不先删除旧值。进程内操作串行，并发首次写入发现已存在时失败，不覆盖竞争者。返回值在 Swift 默认字符串描述中脱敏；桥接结果确实包含状态明文供调用方使用，调用方禁止日志、URL 或其他明文存储。
- 非 iOS 平台目前明确返回 `unsupported_platform`，没有普通文件或软件明文回退。Android 需要独立 Keystore 安全存储实现，小程序认证另行实现。

## 验证

- `docs/evidence/P02/P02-07/verify-synthetic.sh`：退出 0。测试使用内存合成 backend，验证缺失、正常读写、重复清理、无效 JSON、大小限制、存储错误保留原值、损坏数据失败及描述脱敏；没有访问真实 Keychain。首次在受限沙箱调用 `swiftc` 时因默认 clang 缓存目录不可写而退出；指定本次临时 module cache 后编译和运行通过。
- 授权环境运行 `docs/evidence/P02/P02-07/verify-uts.sh`：退出 0。HBuilderX 5.33.2026102401、iPhoneOS SDK 26.5 生成实际 iPhoneOS arm64 未签名 framework；本次二进制 SHA-256 `7f779d2803bc9cc897abe6114ae90bb3ac8cf9fe641cfb26f03789d1f36e77c6` 仅标识该次编译，不代表可复现构建或安装包。
- 独立 reviewer 只读检查 Keychain 查询、操作范围、失败映射、UTS 边界和日志，未发现可复现的 P0–P2；未触碰真实 Keychain 或执行设备操作。

## 未验证

没有真实 iPhone 的 Keychain 创建/读写、属性返回、锁屏、卸载重装或跨进程并发证据；也没有生产登录接线、服务端验证、完整原生 App 打包、签名和安装。项目 CLI 编译器为 5.26，上述 5.33 UTS 编译不能证明锁定版本兼容。后台锁屏时该存储不可读，后台同步策略需单独设计，不应靠降级明文存储解决。
