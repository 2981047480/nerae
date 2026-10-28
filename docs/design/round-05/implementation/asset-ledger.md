# Nerae D02 素材来源与权利台账（草案）

日期：2026-10-28

用途：供 D02-01 / U01–U08 实施拆卡引用。本文基于 `design/prototype/ASSETS.md`、`docs/design/round-05/references/manifest.json` 与 `docs/design/round-05/app-storyboards/README.md` 整理；不下载素材、不补造来源，也不代表正式生产许可、真实对象授权或平台审核已通过。

## 状态定义

- **样机素材 / 来源已记录**：ASSETS.md 记录了来源、作者、许可、转换说明和部分摘要。尚未在本次任务中独立复核 Commons 页面、源文件、署名呈现、人物/场地权利或目标渠道条件。不可仅凭此表认定正式产品获准使用。
- **第三方设计参考**：Appllama manifest 记录页面截图和 SHA-256；只作为设计研究参考。manifest 未记录可再发布或产品内使用许可，不得复制到产品素材或对外传播。
- **生成图板 / 流程示意**：用于内部设计流程预览。图中人物、课程、报价、日期、文字及健康/训练数据是示意，不作真实人物、服务、动作教学或业务事实的证据。
- **缺失 / 待核权**：正式页面需要真实内容或获权素材时，登记具体资产、来源、许可/授权和适用范围；证据齐全前使用下文的缺图替代。

## 样机现有图片（10 张）

ASSETS.md 记录这些图片取自 Wikimedia Commons，下载后等比缩放并转为 WebP（quality 82），未改变画面内容，页面裁切由 CSS `object-fit` 完成。下表的许可与 SHA-256 前缀均为该清单原记录，不是本次独立鉴权结果。

| ID / 本地文件 | 样机用途 | 记录的来源 / 作者 | 清单记录的许可 | 已记录的本地证据 | 关联任务 / 使用边界 |
|---|---|---|---|---|---|
| P-01 `public/assets/hero-today.webp` | S01 今日主视觉 | [Commons 文件页](https://commons.wikimedia.org/wiki/File:Attractive_sporty_woman_doing_overhead_press_in_gym_with_barbell.jpg)；Nenad Stojkovic | CC BY 2.0 | 1400×2097，177 KB；SHA-256 前缀 `6040d1b1df4b` | D02-01 / U01。仅通用训练语境，不代表教练或用户；S03 复用也不表示不同人物。正式复用前按生产渠道复核署名与许可。 |
| P-02 `public/assets/explore-featured.webp` | S02 主推训练封面 | [Commons 文件页](https://commons.wikimedia.org/wiki/File:Athlete_in_the_gym_doing_exercises.jpg)；Shixart1985 | CC BY 2.0 | 1600×1068，103 KB；`cdf90e4265a5` | D02-01 / U06。不是某个真实课程、动作处方或教练履约证明。 |
| P-03 `public/assets/explore-stretch.webp` | 训练后舒展缩略封面 | [Commons 文件页](https://commons.wikimedia.org/wiki/File:Fit_woman_stretching_her_leg_in_gym.jpg)；Shixart1985 | CC BY 2.0 | 900×601，67 KB；`4f4124d0e936` | D02-01 / U06。照片不证明训练效果或特定动作规范。 |
| P-04 `public/assets/explore-plank.webp` | 核心稳定计划缩略封面 | [Commons 文件页](https://commons.wikimedia.org/wiki/File:Fitness_enthusiast_performs_plank_exercise_at_home_on_yoga_mat.jpg)；Shixart1985 | CC BY 2.0 | 900×601，43 KB；`8e5a2e4c720b` | D02-01 / U06。作为情境图；不能替代经审核的动作步骤/安全说明。 |
| P-05 `public/assets/explore-dumbbell.webp` | 哑铃增肌计划缩略封面 | [Commons 文件页](https://commons.wikimedia.org/wiki/File:Woman_standing_in_front_of_a_dumbbell_rack_doing_bicep_curls.jpg)；Shixart1985 | CC BY 2.0 | 900×601，24 KB；`06eabef7cee7` | D02-01 / U06。不得据图宣称某教练、课程或训练结果。 |
| P-06 `public/assets/training-dumbbells.webp` | S04 训练环境素材 | [Commons 文件页](https://commons.wikimedia.org/wiki/File:A_close-up_view_of_a_set_of_heavy_dumbbells.jpg)；Shixart1985 | CC BY 2.0 | 1000×1498，94 KB；`a84808bc7c6b` | D02-01 / U02。器材情境图，不代表用户实际拥有或使用该器材。 |
| P-07 `public/assets/diet-featured.webp` | 饮食主推封面 | [Commons 文件页](https://commons.wikimedia.org/wiki/File:Healthy_salad_bowl_(42231145040).jpg)；Ella Olsson（Stockholm, Sweden） | CC BY 2.0 | 1400×933，215 KB；`77464b5f264f` | D02-01 / U06 / U07。不是实际摄入、份量、营养成分或疗效证据。 |
| P-08 `public/assets/diet-breakfast.webp` | “训练日早餐”缩略封面 | [Commons 文件页](https://commons.wikimedia.org/wiki/File:Healthy_Breakfast_(Unsplash).jpg)；Federica Diliberto / federyka | CC0 | 900×1200，135 KB；`478b3a892e19` | D02-01 / U06 / U07。仅内容情境示意，不与真实饮食记录绑定。 |
| P-09 `public/assets/diet-mealprep.webp` | “休息日备餐”缩略封面 | [Commons 文件页](https://commons.wikimedia.org/wiki/File:Healthy_meal_prep_with_fresh_salad,_fruits,_and_planning_tools_displayed_on_a_table.jpg)；Shixart1985 | CC BY 2.0 | 900×1348，113 KB；`3e96e8042165` | D02-01 / U06 / U07。照片不证明用户摄入、计划达标或营养结果。 |
| P-10 `public/assets/gym-interior.webp` | 器械全身训练计划封面 | [Commons 文件页](https://commons.wikimedia.org/wiki/File:Interior_of_new_modern_gym_with_equipment.jpg)；Shixart1985 | CC BY 2.0 | 1000×666，103 KB；`ce31a007e3a6` | D02-01 / U06。通用场景，不代表某教练的实际场地或服务。 |

**清单记录的附属说明：**CC BY 2.0 项目要求署名、提供许可链接并说明修改；ASSETS.md 称样机已提供图像署名和修改说明。CC0 项目保留来源/作者记录。样机失败回退由 `PhotoFrame.vue` 提供可读替代文本和背景。系统字体、Lucide 图标和内联 SVG 字标不是这 10 张照片，不纳入图片许可行；ASSETS.md 记录字体使用系统字体、Lucide 1.61.0 为 ISC。

## Appllama 第三方截图参考（5 张）

来源根据 manifest 记录；截图仅作内部设计比较，不是 Nerae 产品资产。未在 manifest 中发现可商业复用或再分发的许可凭证。

| ID | 参考 App / 页面 | 本地路径 | 来源记录 | SHA-256 | 使用界限 / 关联 |
|---|---|---|---|---|---|
| R-01 | MasterClass `1273867416` / Home Content Discovery (`oth_pfz55`) | `docs/design/round-05/references/1273867416-oth_pfz55.webp` | manifest 的 `media_url` 指向 Appllama；1170×2532 | `f9e8c98c157eb152f254e812f82d0d2ac4adc13f6a6e879b8f57322eaae5cd1b` | 仅布局/内容组织参考；D02-01。不得将截图、品牌标识或其中照片复用到产品。 |
| R-02 | MasterClass / Course Details (`oth_jhmls`) | `docs/design/round-05/references/1273867416-oth_jhmls.webp` | manifest 记录 Appllama media URL；1170×2532 | `90352c5e2140b419085b24ca895e0dbaa69040ecdcc0a3cb874c233b2d80e2f5` | 同上。 |
| R-03 | Airbnb `401626263` / Services Explore (`oth_fo64p`) | `docs/design/round-05/references/401626263-oth_fo64p.webp` | manifest 记录 Appllama media URL；1170×2532 | `ed68ae826e8873cca5be8185474e407f226e03c5af4cb0a04b63dcdf0c4600d9` | 同上；只研究服务范围/提供者组织，不带入 Airbnb 内容或资产。 |
| R-04 | Airbnb / Chef Service Overview (`oth_bhsic`) | `docs/design/round-05/references/401626263-oth_bhsic.webp` | manifest 记录 Appllama media URL；1170×2532 | `081a5f3aeb3f7f68a0a370fdf159c2e3b8838da7f08fea1d3ef9ea475aaaa92e` | 同上。 |
| R-05 | Airbnb / Service Menu (`oth_ub321`) | `docs/design/round-05/references/401626263-oth_ub321.webp` | manifest 记录 Appllama media URL；1170×2532 | `036f3e3011c2033d9e2f4fb85bd7aae7a7a56e07d2414bf88b63fa12d622131a` | 同上。 |

## 六张生成/渲染图板

README 记录共 6 张、30 个手机画面：5 张使用内置 image_gen，记录图因生成修订路径返回 403，改用可编辑 HTML 按同一视觉规则渲染；图板中的人物、内容、日期、价格与文字为示意；屏 10 的“约 60 分钟”不是实际训练时长，不应推算或写入训练记录。README 指向 `prompts.json`、`03-recording.source.html`、`manifest.json` 和预览 Zip 作为提示词、来源、尺寸/hash及归档依据。图板不是已获许可的真人肖像、真实教练/学员资料、课程、营养方案或生产 UI。

| ID | 图板路径 | 内容 / 资产状态 | 关联任务与限制 |
|---|---|---|---|
| B-01 | `docs/design/round-05/app-storyboards/01-discover.png` | 发现与选择；样例图像/文字 | D02-01；U01 / U06。生成图板，内含素材不当作课程或内容事实。 |
| B-02 | `docs/design/round-05/app-storyboards/02-service.png` | 咨询、服务确认与计划接收；报价/服务为示意 | D02-01；U04 / U05 / A08。不得作为教练供给、资质、真实报价或履约证据。 |
| B-03 | `docs/design/round-05/app-storyboards/03-recording.jpg` | 训练记录；HTML 按 UI 规则渲染，非生成图 | D02-01；U02。记录界面的流程参考；画面示例不能替代服务端记录。 |
| B-04 | `docs/design/round-05/app-storyboards/04-diet-ai.png` | 饮食自评与 AI 辅助；内容/建议为示意 | D02-01；U06 / U07。不可作为营养事实、摄入记录或医疗建议。 |
| B-05 | `docs/design/round-05/app-storyboards/05-coach.png` | 教练交付闭环；身份和内容为示意 | D02-01；U03 / A08。不能证明真实教练身份、认证或学员授权。 |
| B-06 | `docs/design/round-05/app-storyboards/06-entry-recovery.png` | 首次进入、采集同意、撤权与恢复；手机号等为示意 | D02-01；U01 / U08 / A01。不能当成真实登录提供商、合同、同意或注销流程已经核定。 |

README 明确部分生成图的小字、图标和示例日程仍需实现时按规范细化；图板没有提升为正式视觉接受。生成图权利和具体平台可用性没有在指定资料中形成单独法律许可凭证，按“仅流程示意 / 生产使用待核”管理。

## 正式资产缺口与暂行替代

| 资产类别 / 需要位置 | 当前缺口与证据边界 | 关联执行任务 | 许可/授权证据通过前的替代 |
|---|---|---|---|
| 真实教练肖像、姓名与认证佐证（发现/服务/教练工作，图板 04–05、21–25） | ASSETS.md 说明不以陌生人像冒充教练，现用姓名首字字标；资质尚未核验。缺少与真实教练身份绑定的本人授权肖像、头像用途/渠道范围、认证事实证据。 | D02-01；U05；A08；平台外联系另受 L01-02 / V02 控制 | 继续用首字字标或中性无人物占位；资质用明确“尚未核验”，不展示假头像/徽章。 |
| 真实服务包、教练工作室/场地及交付照片（发现/教练详情/服务包，屏 04–09、25） | 10 张照片不证明特定服务包、教练或场地；Appllama 的 Airbnb 截图是第三方参考，不是服务实拍。缺少服务提供者关联、拍摄/素材来源、本人/场地许可和使用范围。 | D02-01；U05 / A08 | 隐藏照片或使用中性服务类型图形与真实文字交付清单；无真实报价/内容数据时不填样例成交和评价。 |
| 经审核的动作图片/动作步骤或视频（训练内容详情、教练计划，屏 03、11–12、21–24） | 现有动作照片只是通用情境封面；未见逐动作对应的演示素材、动作来源、专业审核/授权证据。不可用 Commons 照片充当规范动作演示。 | D02-01；U02 / U06；训练内容由对应 S01-A / S01-B 任务按源内容责任补证 | 先用文字名称、组数/实际记录和不含演示图的中性占位；动作解释缺失时隐藏演示位，不随机补图、不生成专业动作事实。 |
| 与真实发布内容绑定的训练/饮食封面（发现与内容详情，屏 02–03、16–17） | 样机十图有训练/饮食情境摄影，但不是已发布内容、版本、作者或其授权的封面绑定；不能把样机照片当真实课程/食谱内容。 | D02-01；U06；内容资料/许可由 S01-A / S01-B 内容任务确认 | 显示真实标题/来源和中性缺图背景；内容不可用时标“暂无图片”或隐藏图片区域，保留可访问内容入口。 |
| 真实饮食/餐盘图与成分事实关联（饮食自评/内容/建议，屏 16–20） | `diet-*` 为 Commons 样例摄影，不能映射为用户真实摄入、分量、营养或达标；没有具体饮食内容图的版本/作者授权证据。 | D02-01；U06 / U07；S01-B | 自评默认不显示照片；内容封面使用中性图形或明确“示意图”；未知成分/份量保持未知，不从照片推断。 |
| 账号、同意、撤权及恢复图中的个人/健康数据 | B-06 是状态示意，不是用户同意书、真实身份验证或获准的登录界面。 | D02-01；U08 / A01 / O03；身份方式依赖 K01-02/03 | 只展示去标识的状态文字和清楚占位；具体提供商、留存/删除承诺和同意范围待合同与正式依据，未核定不承诺。 |

### 入库前的许可证据字段

每个正式资产在内容系统或随资产登记中至少要记录：稳定资产 ID、文件路径/版本、原始来源 URL/作者、取得日期、许可证原文及适用范围、商用/改编/再分发条件、必须署名文字及呈现位置、具体改动、人物肖像/场地/商标等单独授权、关联真实内容/教练及审核人、许可覆盖的三端/渠道、文件 SHA-256、失效或撤回处理。CC BY 清单记录不自动证明肖像、场地或商标权已获准；第三方页面截图与生成图板均不替代正式素材授权。

## 执行边界与关联任务

- **D02-01**：维护本表与 UI_SPEC 可追溯索引，标明示意、缺图和素材权利边界；不把设计输入写成已通过的法律审核或生产资产。
- **U01–U08**：具体实现卡在细拆时引用 D02 页面/状态、素材 ID、来源状态和三端截图；每个资产进入正式页面前补齐证据。对应 U05/A08 负责教练与服务包，U06 负责训练/饮食发现与发布，U07 负责饮食自评/建议，U08 负责账号与数据控制。
- **L01-02 / V02**：处理平台主体、类目、渠道与双人应用验收等外部条件，不替代具体图片版权/肖像/场地许可。未获得所需素材证据时由 UI 使用上述无图替代。
- **不得外推**：本表没有复核线上 Commons 页面状态、原图版权链、人物/场地授权、第三方截图分发条款或生成图的生产商用权；没有任何条目因此自动变为“生产许可通过”。
