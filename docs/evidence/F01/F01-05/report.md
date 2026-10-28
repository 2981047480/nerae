# F01-05 基础检查进度（未接受）

- 状态：`partial`。执行者为 Project Lead；运行时未报告主代理的精确型号和推理强度。任务基线 `0106cf37cc6e804478e6b249612b6c041880d904`；实际验证的集成提交 `31c174da09e5be4e0493f2bdb2b75f76b6d8493c`。F01-04 隔离验收尚未完成，本卡仍为 `active`，不能将 F01-I 或 F01 标为完成。
- 新增 Make 入口、GitHub Actions 工作流、检查脚本与 `docs/engineering/ci-gates.md`。CI 与本地采用同一个 `make check-foundation`；CI 的 YAML 结构已解析，但尚无 GitHub 托管运行证据。构建只生成客户端资源和后台静态文件，不是可安装原生 App、真机或业务验收。
- 依赖边界用 Go 语法解析和 TypeScript/Vue 解析跟踪，新增正反例覆盖跨模块存储、构建标签、未审核客户端 SDK、源码别名、动态 import、`import.meta.glob`、生产代码导入 `testdata`、Worker / Service Worker / 小程序 Worker 等。暂不支持的动态图直接失败。检查只验证约定的静态导入规则；不能证明数据库表写入归属、运行时授权、网络访问或实际依赖注入，仍须业务和集成验收。
- 为避免空绿灯：构建输出使用新目录并核对指定文件非空；缺工具、空测试、全跳过或预期失败、无产物、类型错误、未就绪目标和非法依赖均有失败注入。`codegen-check`、`check-isolation`、`migration-check`、业务 `test`、`test-integration`、E2E、视觉、种子与总 `check` 保持非零 `NOT_READY`。

## 在集成提交的实际验证

- 锁定环境：Node 26.0.0、pnpm 11.5.3、Go 1.27.3、Python 3.11.15。冻结离线安装从本地缓存复用 506 个包，下载 0，未修改锁文件。
- `make check-foundation`：退出 0；47 项任务图测试、97 项静态合同测试、8 项检查入口及失败注入测试、19 项环境脚本测试，全部通过。Go `vet` 与 race 测试退出 0，后者有 21 个通过的叶子测试；TS/JS 与 Vue 脚本格式检查 7 个文件，格式反例 4 项通过；Go/TS 类型及格式检查、边界反例 17 项均通过。合同套件的摘要与非空检查各执行一次。
- 同一次构建在新目录生成并检查 H5 `index.html` 473 字节、小程序 `app.js` 267 字节、App 资源 `app-service.js` 1184 字节、后台 `index.html` 324 字节。临时产物保留在 `/var/folders/6k/klwbxv35003_wd7b9kwtnt6c0000gn/T/nerae-foundation-build-hatyzjk7` 与 `nerae-foundation-build-8nv2_fk3`；完整日志 `/private/tmp/nerae-f01-ci-integrated-31c174d.log`。
- 任务工作树首次完整检查在 H5 编译时因沙箱禁止 Vite 写临时配置而退出 2；提高该工作树运行权限后同一入口退出 0。该失败记录保留在 `/private/tmp/nerae-f01-ci-foundation-20261029.log`，不作为产品构建失败或已通过证据。
- 独立 reviewer `/root/foundation_review` 审核 Make、构建产物、TS 格式与失败注入，指出缺少产物真实性和格式检查，修复后复核未发现新的重大问题。`/root/contract_tests` 多轮审核并实际复跑边界反例，指出别名、跨域存储和动态模块等遗漏；修复后定向复核通过、17 项测试退出 0。审阅的任务提交 `8849c3dae69dba88da36fc3be198a5709c9a26a9` 与集成提交 `31c174da09e5be4e0493f2bdb2b75f76b6d8493c` 的本卡文件逐项一致；完整检查在后者重跑。

## 剩余条件

F01-04 的真实 Compose 项目和卷清理隔离尚未验证，当前没有可调用 Docker 运行时；对象存储隔离、业务数据库、原生安装/真机以及 GitHub 托管 CI 尚无通过证据。DCloud 开发者账号与 Android 真机目前不可用；这些不妨碍先完成独立基础工作。用户/App 职责与两种训练记录语义未改变。
