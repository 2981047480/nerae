# F01-06 客户端 URL 兼容验证

状态：active。`core-js@3.51.0` 原已存在于锁文件的传递依赖中，现作为客户端精确版本的直接运行依赖，并在 `apps/client/src/main.ts` 最前加载 `core-js/actual/url`。其他依赖版本未变；`pnpm install --lockfile-only --offline --ignore-scripts` 退出 0，锁文件只新增客户端依赖条目。

`verify-jsc-url.sh` 退出 0。脚本在临时目录打包该依赖及项目真实 `EmailAuthApi`、`RecordingApi` 源码，在没有 `URL` 的 macOS JavaScriptCore 裸上下文中运行：检查 HTTPS origin、默认及显式端口、IPv4、查询、路径归一化、无效 URL，以及两个适配器的正常构造与非法地址拒绝。测试只用合成地址，未联网。系统曾输出 Xcode 缓存和 FSEvents 警告，但探针退出 0。

`verify-resources.sh` 退出 0：使用现有离线 5.26 Vue3 工具链与本机 `vue-tsc`，在临时客户端副本通过完整类型检查，生成 App 与微信小程序 JS 资源，验证原入口的 URL polyfill 和临时探针均被打入资源；小程序 polyfill 位于被 app.js 加载的 `common/vendor.js`。最初资源检查误以为微信 polyfill 必须位于 app.js 而退出 1，失败目录保留在 `/private/tmp/nerae-f01-06-resources.thgJadZG`；检查实际 vendor 路径并修正验证器后复跑通过。

边界：裸 macOS JavaScriptCore 不等于 iPhone 上 DCloud App runtime；资源构建未链接 iOS/Android UTS，未生成 IPA/APK，也未安装或触发真实请求。全局 URL/timer 与原生网络桥仍需实际 App 运行验证。敏感凭据、真实邮箱、服务器均未参与测试。
