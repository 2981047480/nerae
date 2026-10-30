# 5.26 UTS 插件导入与资源构建验证 · 2026-10-31

## 问题与修复

在隔离夹具中临时安装与项目同版的 `@dcloudio/uni-uts-v1` 后，直接从 `nerae-device-key/utssdk` 导入的 App 构建虽退出 0，实际 `app-service.js` 包含 `unsupported_platform`，未产生原生 UTS 代理及源码。改为从插件根目录导入后，同一编译器生成了代理及 iOS/Android 源码。项目已有 HTTPS 接线使用了同类内部目录导入，因此改为插件根目录导入，并为客户端固定同版 UTS 编译依赖。

## 验证

- `pnpm install --filter @nerae/client --frozen-lockfile --offline --ignore-scripts --store-dir /private/tmp/nerae-pnpm-store`：退出 0；只证明当前机器缓存和锁文件一致，不证明全新机器离线可安装。
- `pnpm --filter @nerae/client run typecheck`：退出 0。
- `python3 scripts/quality/verify_uts_plugin_build.py`：退出 0。复制客户端源码到独立临时夹具并以锁定的 5.26 编译；检查设备密钥、随机数、安全存储及 HTTPS 四个插件的 App JS 原生代理及 iOS Swift/Android Kotlin wrapper，另检查微信小程序的明确不支持回退。HTTPS 通过实际 `platform/app` 入口导入；探针页面仅用于编译，不进入产品。
- 实际客户端当前空入口的 `build:app` 与 `build:mp-weixin` 均退出 0；由于它尚未接入业务，不能用来证明插件或功能可用。
- 夹具保留在系统临时目录，以便检查；没有访问真实邮箱、网络服务或设备密钥。

## 边界

资源生成不是 UTS 原生 framework/Android 运行时链接、签名安装或真机执行。设备密钥、安全 vault 和 HTTPS 均尚未完整接入登录页面；可安装包、真实登录、跨设备同步及两种记录界面仍未完成。iOS 离线打包仍需匹配的原生 SDK、AppKey 和签名路径。
