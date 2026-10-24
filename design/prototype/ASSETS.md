# 素材来源与许可
本目录 `public/assets/` 的图片均从 Wikimedia Commons 取得，下载后等比缩放并转为 WebP（quality 82），未改变画面内容；页面裁切由 CSS `object-fit` 完成。所有图片仅用于训练与内容语境，不代表任何真实学员、教练身份、认证、成绩或背书。
| 本地文件 | 用途 | 来源（Commons 文件页） | 作者 | 许可 | 尺寸 / 大小 | SHA-256（前 12 位） |
|---|---|---|---|---|---|---|
| `public/assets/hero-today.webp` | S01 今天主面摄影（CSS 裁切） | [文件页](https://commons.wikimedia.org/wiki/File:Attractive_sporty_woman_doing_overhead_press_in_gym_with_barbell.jpg) | Nenad Stojkovic | [CC BY 2.0](https://creativecommons.org/licenses/by/2.0) | 1400x2097 / 177 KB | `6040d1b1df4b` |
| `public/assets/explore-featured.webp` | S02 探索主推训练内容封面 | [文件页](https://commons.wikimedia.org/wiki/File:Athlete_in_the_gym_doing_exercises.jpg) | Shixart1985 | [CC BY 2.0](https://creativecommons.org/licenses/by/2.0) | 1600x1068 / 103 KB | `cdf90e4265a5` |
| `public/assets/explore-stretch.webp` | S02 训练后舒展计划缩略封面 | [文件页](https://commons.wikimedia.org/wiki/File:Fit_woman_stretching_her_leg_in_gym.jpg) | Shixart1985 | [CC BY 2.0](https://creativecommons.org/licenses/by/2.0) | 900x601 / 67 KB | `4f4124d0e936` |
| `public/assets/explore-plank.webp` | S02 核心稳定计划缩略封面 | [文件页](https://commons.wikimedia.org/wiki/File:Fitness_enthusiast_performs_plank_exercise_at_home_on_yoga_mat.jpg) | Shixart1985 | [CC BY 2.0](https://creativecommons.org/licenses/by/2.0) | 900x601 / 43 KB | `8e5a2e4c720b` |
| `public/assets/explore-dumbbell.webp` | S02 哑铃增肌计划缩略封面 | [文件页](https://commons.wikimedia.org/wiki/File:Woman_standing_in_front_of_a_dumbbell_rack_doing_bicep_curls.jpg) | Shixart1985 | [CC BY 2.0](https://creativecommons.org/licenses/by/2.0) | 900x601 / 24 KB | `06eabef7cee7` |
| `public/assets/training-dumbbells.webp` | S04 专注训练的动作环境素材 | [文件页](https://commons.wikimedia.org/wiki/File:A_close-up_view_of_a_set_of_heavy_dumbbells.jpg) | Shixart1985 | [CC BY 2.0](https://creativecommons.org/licenses/by/2.0) | 1000x1498 / 94 KB | `a84808bc7c6b` |
| `public/assets/diet-featured.webp` | S02 饮食内容主推封面 | [文件页](https://commons.wikimedia.org/wiki/File:Healthy_salad_bowl_(42231145040).jpg) | Ella Olsson from Stockholm, Sweden | [CC BY 2.0](https://creativecommons.org/licenses/by/2.0) | 1400x933 / 215 KB | `77464b5f264f` |
| `public/assets/diet-breakfast.webp` | S02 饮食内容「训练日早餐」缩略封面 | [文件页](https://commons.wikimedia.org/wiki/File:Healthy_Breakfast_(Unsplash).jpg) | Federica Diliberto federyka | [CC0](http://creativecommons.org/publicdomain/zero/1.0/deed.en) | 900x1200 / 135 KB | `478b3a892e19` |
| `public/assets/diet-mealprep.webp` | S02 饮食内容「休息日备餐」缩略封面 | [文件页](https://commons.wikimedia.org/wiki/File:Healthy_meal_prep_with_fresh_salad,_fruits,_and_planning_tools_displayed_on_a_table.jpg) | Shixart1985 | [CC BY 2.0](https://creativecommons.org/licenses/by/2.0) | 900x1348 / 113 KB | `3e96e8042165` |
| `public/assets/gym-interior.webp` | S02 器械全身训练计划封面 | [文件页](https://commons.wikimedia.org/wiki/File:Interior_of_new_modern_gym_with_equipment.jpg) | Shixart1985 | [CC BY 2.0](https://creativecommons.org/licenses/by/2.0) | 1000x666 / 103 KB | `ce31a007e3a6` |

## 许可说明

- CC BY 2.0：允许共享与改编（包括商业用途），须署名、注明许可并提供许可链接，并说明是否作出修改。本目录已在预览“图像署名”和本文件提供署名与修改说明。
- CC0：作者放弃版权，仍在本文件保留来源与作者记录。
- 未使用随机图片 API；运行时只读取本地文件，不向第三方请求图片。

## 字体与图标

- 字体：使用系统中文字体（PingFang SC / Noto Sans SC / Source Han Sans SC 等）与系统无衬线，不加载外部字体文件，无字体许可文件需要分发。
- 图标：`@lucide/vue` 1.61.0（Lucide，ISC License），按需引入线性图标，未使用 emoji 或第二套图标。
- 站点图标：`index.html` 内联 SVG 字标，不含第三方素材。

## 若图片加载失败

`PhotoFrame.vue` 提供真实失败替代文本与可读背景，不把损坏图片当作完成。
