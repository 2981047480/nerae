# 开发任务目录

由 `scripts/plan.py catalog` 从 tasks.json 生成；JSON是唯一状态源。work_package不能直接派发；task/integration才是执行卡，单卡不超过3有效人日。估工不含外部等待，不是agent运行时间或日历承诺。微信小程序、iOS与Android App同步纳入；正式U01–U08等待工程、R05实施规格和依赖证据。

| ID | 用户结果 / 任务 | 类型/工作包 | 开始依赖 | 接受前置 | 人日假设 | 状态 |
|---|---|---|---|---|---|---|
| F01 | 可复现工程与隔离环境 | work_package / complete | F01-01, F01-02, F01-03, F01-04, F01-05, F01-I | — | 6–11 | planned |
| F02 | 生成契约和统一 transport 边界 | work_package / complete | F01, F02-01, F02-02, F02-03, F02-I | — | 3.5–8.5 | planned |
| D01 | 视觉基线与主路径可用性评审 | work_package / complete | D01-01, D01-02, D01-03, D01-I | — | 3.5–7 | accepted |
| L01 | 取得上线、联络及数据处理依据 | work_package / complete | L01-01, L01-02, L01-03, L01-I, L01-04 | — | 4.5–9 | planned |
| A01 | 身份、年龄与最小档案 | work_package / pending | F02 | — | 3–5 | planned |
| A02 | 服务关系与即时撤权 | work_package / pending | A01 | — | 4–7 | planned |
| A03 | 动作库、私人计划与教练发布 | work_package / pending | I-A01 | — | 4–7 | planned |
| A04 | 日历、训练记录与可靠组数据 | work_package / pending | A03 | — | 6–10 | planned |
| A05 | 固定 MiMo 配置与教练 AI 草稿 | work_package / pending | A03 | L01-04 | 5–8 | planned |
| A06 | 关系会话、咨询消息与站内未读 | work_package / pending | I-A01 | — | 4–7 | planned |
| A07 | 教练待办与按授权的学员读模型 | work_package / pending | A04, A06 | — | 3–5 | planned |
| A08 | 教练供给、咨询与来源记录 | work_package / pending | I-A01 | — | 4–6 | planned |
| O01 | 私有文件、认证审核与最小运营台 | work_package / pending | A01 | — | 4–7 | planned |
| U01 | 设计系统与多端壳层 | work_package / pending / 暂缓 | F02, D02, P02-01 | P02-02, P02-03 | 4–6 | planned |
| U02 | 学员今天与训练记录界面 | work_package / pending / 暂缓 | A04, U01 | — | 5–8 | planned |
| U03 | 教练继续草稿、发布与待办 | work_package / pending / 暂缓 | A05, A07, U01 | — | 5–8 | planned |
| U04 | 关系消息与通知界面 | work_package / pending / 暂缓 | A06, U01 | — | 3–5 | planned |
| U05 | 教练发现、咨询预填与关系确认界面 | work_package / pending / 暂缓 | A08, O01, U01 | — | 4–6 | planned |
| I01 | 集成首个可运行交付闭环 | work_package / partial | U02, U03, U04, U05, I-A01, I-A02, I-A03, I-A04 | — | 4.5–10.5 | planned |
| V01 | P0-A 运行时、视觉与恢复验收 | work_package / pending | I01, S01-A | — | 3–5 | planned |
| V02 | 陌生获客渠道双端验收 | work_package / pending | V01, L01, U08 | — | 1–3 | planned |
| B01 | 训练饮食模板广场与独立副本 | work_package / pending | A03 | — | 3–5 | planned |
| B02 | 发布助手与审核链 | work_package / pending | B01, A05, O01 | — | 4–7 | planned |
| B03 | 饮食自评、观察与证据不足分析 | work_package / pending | A05, A07 | — | 5–8 | planned |
| B04 | 模板内建议及显式应用 | work_package / pending | B01, A05 | — | 3–5 | planned |
| B05 | 收藏分享热门与可见性失效 | work_package / pending | B01 | — | 2–4 | planned |
| U06 | 计划广场与发布编辑界面 | work_package / pending / 暂缓 | B02, B05, U02 | — | 4–7 | planned |
| U07 | 饮食打卡与建议确认界面 | work_package / pending / 暂缓 | B03, B04, U02 | — | 4–6 | planned |
| O02 | 评价、举报与可审计申诉 | work_package / pending | A08, O01 | — | 3–5 | planned |
| O03 | 可观测性、备份恢复与隐私运维 | work_package / pending | A06, O01 | L01-03 | 4–7 | planned |
| I02 | 集成完整 P0-B 与运营依赖 | work_package / partial | V01, U06, U07, O02, O03, I-B01, I-B02 | — | 3–5 | planned |
| V03 | P0-B 功能视觉与安全验收 | work_package / pending | I02, S01-B | — | 3–5 | planned |
| V04 | 分来源试点与成熟窗口判断 | work_package / pending | V02, V03, O03 | — | 3–6 | planned |
| P01 | 周报、邀请与必要媒体扩展 | work_package / pending | V03 | — | 5–10 | planned |
| P02 | iOS / Android App 能力与三端交付 | work_package / partial | V03, P02-01, P02-02, P02-03 | — | 6–13 | planned |
| M01 | 维护节奏、依赖升级与恢复演练 | work_package / pending | V03 | — | 2–4 | planned |
| S01 | 可交付种子内容与来源审核 | work_package / complete | S01-A, S01-B | — | 2–6 | planned |
| F01-01 | Git基线、缓存忽略与工具链兼容锁定 | task / F01 | — | — | 0.5–1 | accepted |
| F01-02 | Go进程、配置与可验证健康检查骨架 | task / F01 | F01-01 | — | 1–2 | accepted |
| F01-03 | 客户端和后台构建骨架 | task / F01 | F01-01 | — | 2–3 | accepted |
| F01-04 | 双工作区数据隔离与迁移执行器 | task / F01 | F01-02 | — | 1–2 | active |
| F01-05 | 统一Make入口、分层检查和CI失败语义 | task / F01 | F01-02, F01-03, F01-04 | — | 1–2 | planned |
| F01-I | 基础工程小批集成与干净检出验收 | integration / F01 | F01-05 | — | 0.5–1 | planned |
| F02-01 | 固定契约生成及可复现验证 | task / F02 | F01, K01 | — | 1–2 | planned |
| F02-02 | 统一错误、请求校验与身份上下文边界 | task / F02 | F02-01 | — | 1–2 | planned |
| F02-03 | 持久化幂等、并发与失败恢复适配器 | task / F02 | F02-02 | — | 1–3 | planned |
| F02-I | transport接线与CI门禁小批集成 | integration / F02 | F02-03 | — | 0.5–1.5 | planned |
| L01-01 | 外部材料缺口与责任清单 | task / L01 | — | — | 0.5–1 | active |
| L01-02 | 主体、类目、手续和允许联系渠道取证 | task / L01 | L01-01 | — | 1.5–3 | planned |
| L01-03 | 分类留存、注销和恢复依据 | task / L01 | L01-01 | — | 1–2 | planned |
| L01-I | 外部证据合并复核与受控放行配置 | integration / L01 | L01-02, L01-03, L01-04 | — | 0.5–1 | planned |
| S01-A | 训练交付最小内容集与幂等导入 | task / S01 | A03 | — | 1–3 | planned |
| S01-B | 广场与饮食内容基线及升级验证 | task / S01 | B01, B03, S01-A | — | 1–3 | planned |
| I-A01 | 身份与服务关系小批HTTP集成 | integration / I01 | A01, A02 | — | 0.5–1.5 | planned |
| I-A02 | 计划与日程训练小批HTTP集成 | integration / I01 | A03, A04, S01-A, I-A01 | — | 1–2 | planned |
| I-A03 | 咨询会话与运营权限小批HTTP集成 | integration / I01 | A06, A08, O01, I-A02 | — | 1–2 | planned |
| I-B01 | 发布分享与申诉小批HTTP集成 | integration / I02 | V01, B01, B02, B05, O02 | — | 1–2 | planned |
| I-B02 | 饮食证据与建议应用小批HTTP集成 | integration / I02 | B03, B04, S01-B, I-B01 | — | 1–2 | planned |
| D01-01 | 设计逻辑逐项推导与开发文稿 | task / D01 | — | — | 0.5–1 | accepted |
| D01-02 | 指定DeepSeek完成四种代表情境 | task / D01 | D01-01 | — | 1–2 | accepted |
| D01-03 | 指定DeepSeek扩展全量情境与状态 | task / D01 | D01-02 | — | 1.5–3 | accepted |
| D01-I | 第三轮UI独立验收与集成 | integration / D01 | D01-03 | — | 0.5–1 | accepted |
| K01 | 三端身份与训练记录合同收敛 | work_package / complete | K01-01, K01-02, K01-03, K01-I | — | 4.5–9.5 | planned |
| K01-01 | 训练后与逐组记录合同 | task / K01 | — | — | 1.5–3 | accepted |
| K01-02 | 小程序与 App 身份及安全关联合同 | task / K01 | K01-01, L01-01, D01 | — | 1.5–3 | planned |
| K01-03 | 账号注销与撤权合同 | task / K01 | K01-02 | — | 1–2 | planned |
| K01-I | 训练、身份与注销合同复核 | integration / K01 | K01-01, K01-02, K01-03 | — | 0.5–1.5 | planned |
| D02 | 第五轮三端设计实施规格 | work_package / complete | D02-01, D02-I | — | 2–3.5 | planned |
| D02-01 | 整理 R05 三端组件与状态规格 | task / D02 | D01 | — | 1.5–2.5 | accepted |
| D02-I | R05 设计实施规格复核 | integration / D02 | D02-01 | — | 0.5–1 | planned |
| P02-01 | App 编译、打包路径与平台缺口探针 | task / P02 | F01-03 | — | 1–3 | accepted |
| Q01 | 陌生执行者任务包上手验证 | work_package / complete / 暂缓 | Q01-01 | — | 0.5–1 | planned |
| Q01-01 | F02-01 任务包冷启动试验 | task / Q01 | F02-01 | — | 0.5–1 | planned |
| L01-04 | AI 供应商数据处理与预算依据 | task / L01 | L01-01 | — | 1–2 | planned |
| P02-02 | iOS 可安装开发包与真机链路 | task / P02 | P02-01 | — | 1–3 | planned |
| P02-03 | Android 可安装开发包与真机链路 | task / P02 | P02-01 | — | 1–3 | planned |
| U08 | 账号关联、注销与数据控制界面 | work_package / pending / 暂缓 | A01, U01 | O03 | 2–4 | planned |
| I-A04 | 三端训练记录与内部试用集成 | integration / I01 | I-A02, U01, U02, P02-02, P02-03 | — | 1–3 | planned |
