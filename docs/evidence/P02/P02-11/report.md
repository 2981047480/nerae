# P02-11 App 原生 HTTPS 适配接缝

状态：App transport adapter 已集成；集成版客户端类型检查与认证、记录、适配器合成测试通过。真实原生 UTS 链接和真机/服务请求未验收。没有接页面、账号状态或生产服务地址。原工作树基线 `8895dd34a28968bf3f4c9577c23e4ad50a0d7c8f`。

## 实现

- `apps/client/src/platform/app/index.ts` 实际从 `@/uni_modules/nerae-https/utssdk` 导入 `request` 与 `cancel`，导出显式传入 HTTPS base 的 App factory。UTS import 的 TS 声明只位于本平台目录；没有修改共享 UTS interface。小程序没有导入 App transport。
- `nativeHttpsTransport.ts` 将原生回调适配为 `SecureAuthTransport` 与 `RequestPort`。只允许 HTTPS 同源且路径位于配置 base path 内；拒绝跨源、降级、片段、路径前缀相似和编码分隔符逃逸。请求 body 字符串原样传递，原有 headers/Idempotency-Key 不重写；不生成 key 或凭据，也不重试。回调只接收一次；处理同步成功/失败、同步抛错、重复回调、原生错误以及缺回调 watchdog。错误映射到固定 Auth/Recording 错误，不复制原生错误内容。UTF-8 长度通过显式、按码点计数的有界函数计算，不在模块加载时依赖 `TextEncoder`；字符串先拒绝未配对 surrogate。
- 对原生 body 与 `headersJson` 先按 UTF-8 字节限长，再用递归解析器完整校验单个 JSON 值、对象形状和深度。解析器对所有嵌套对象检测重复键，并按解码后的键比较，因此转义同名键也拒绝；拒绝畸形 UTF-8/转义、非有限数字、尾随多值和非对象 body。headers 限为桥接器 allowlist、字符串值和尺寸上限。合法响应的 status 与解析对象原样交给现有业务 API 校验；例如 401 保持 401，不变成成功。

## 验证

- `node --import ../../docs/evidence/P02/P02-11/node-ts-loader.mjs --test src/platform/app/nativeHttpsTransport.test.mjs`（cwd `apps/client`）：退出 0，16 项通过。覆盖原始 body/幂等键、GET query、路径越界、同步/重复回调、抛错、超时与 cancel、错误脱敏、重复/转义冲突键、深层/多值/非对象 JSON、headers 校验、响应过大和非成功 HTTP 状态；新增 4 MiB UTF-8 精确边界与超限反例，使用 `💥` 字符证明按字节而非 UTF-16 长度计算。全部通过注入的合成 native port，不是真实网络。
- `tsc --strict --noEmit --target ES2022 --module ESNext --moduleResolution Bundler --lib ES2022,DOM --skipLibCheck src/platform/app/nativeHttpsTransport.ts src/platform/app/index.ts src/platform/app/neraeHttpsUts.d.ts /private/tmp/nerae-p02-11-uni-lite.d.ts`（cwd `apps/client`）：退出 0，使用本机现存 TypeScript 5.9.3。临时声明只补齐缺失的全局 `uni`，以检查本适配器依赖闭包；不等于完整客户端 typecheck。
- `/Users/zephyr/.codex/worktrees/nerae-recording-redesign/nerae/design/prototype/node_modules/.bin/vue-tsc --noEmit -p tsconfig.json`（cwd `apps/client`）：退出 2；该 worktree 没有 `apps/client/node_modules`，报 `TS2688: Cannot find type definition file for '@dcloudio/types'`。没有安装依赖或改锁文件。
- `NERAE_CLIENT_NODE_MODULES=/private/tmp/nerae-f01-03-client-build.m1zTMY/node_modules bash docs/evidence/P02/P02-10/verify-weixin-resource.sh`：退出 0；项目锁定 CLI `5.26（vue3）` 输出小程序 `app.js` 并检查 unsupported fallback。此构建与 App adapter 分离，未导入/借用 App HTTPS 实现。
- `NERAE_CLIENT_NODE_MODULES=/private/tmp/nerae-f01-03-client-build.m1zTMY/node_modules bash docs/evidence/P02/P02-11/verify-app-resource.sh`：最终退出 0；锁定 CLI `5.26（vue3）` 在临时副本的 App entry 实际 import 并调用 factory、将 factory/两个 adapter port/原生 request 与 cancel binding 暴露到临时 `globalThis` probe。脚本再用 Node VM 执行生成的 `app-service.js`、调用两个 adapter port，并观察到当前资源构建器发出的 `unsupported_platform` 结果；产出非空 `app-service.js`（9720 bytes）。这证明生成资源保留并执行了 JS adapter 与该平台 fallback；不是 iOS/Android 原生 UTS 链接、APK、IPA 或已安装 App。首次验证器因缺少 VM timer shim 和 Vue app stub 失败，分别保留现场 `/private/tmp/nerae-p02-11-app.sjo5lgvK`、`/private/tmp/nerae-p02-11-app.UXdI3Qyp`；补齐仅供 VM 的 shim 后重跑通过。成功后脚本只删除自身临时目录。
- `/private/tmp/nerae-hbuilder-download/HBuilderX.app/Contents/MacOS/cli --help`：退出 134，输出 Qt `Incompatible processor ... requires ... neon`。因此未用该 HBuilderX CLI 验证实际 iOS/Android UTS 链接；没有启动模拟器或设备。项目本地锁定 CLI 资源构建成功不能填补该差异。
- App runtime 兼容边界：离线 DCloud 包源码不能证明目标 App JavaScriptCore 默认提供 `URL`；本适配器及现有认证、训练 API 都使用 `new URL()`。后续 F01-06 已在客户端入口显式装入 URL polyfill，并用裸 JavaScriptCore 和本卡 VM 资源探针验证，但尚未在 DCloud App 真机确认。生成的 App bootstrap 将 `setTimeout`/`clearTimeout` 传给 `uni.restoreGlobal(...)`；watchdog 依赖 timer，其真机行为仍待验证。
- `git diff --check`：退出 0。

集成版本 `4099dde` 后，客户端显式引入锁定的 core-js URL polyfill。更新的 App 资源验证脚本在没有预注入 URL 的 VM 中执行实际生成的 App JS，验证 polyfill 提供 URL、两种端口被保留并返回该构建实际选择的 unsupported fallback；退出 0，资源为 53227 bytes。此前独立 Android 原生桥已合入，但此离线构建没有链接它。HBuilderX 原生导出、真实 App 运行、生产 HTTPS base/证书、真实服务和页面接线仍待单独验证。合成测试无法证明 native bridge 的实际 callback 生命周期。
