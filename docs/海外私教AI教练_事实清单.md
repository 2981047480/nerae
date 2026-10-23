# 海外「线上私教 + AI 教练」紧凑事实清单

**1) 线上私教**
- Future：$199/月（首月$50），1:1 教练 + Apple Watch 数据；2020 年 $24M B 轮；现用户/营收未查到可靠数据。[future.co](https://future.co/)｜[TechCrunch](https://techcrunch.com/2020/10/16/future-raises-24m-series-b-for-its-150-mo-exercise-app-amid-at-home-fitness-boom/)
- Caliber：免费记录 + $19/月团体 + $200 起 1:1；2020 年 $2.2M 种子。[BarBend](https://barbend.com/caliber-fitness-app-review/)｜[TechCrunch](https://techcrunch.com/2020/10/13/caliber-with-2-2-million-in-seed-funding-launches-a-fitness-coaching-platform/)
- Kickoff：保险报销，多数用户 $0，营养师+私教双证，每周视频+每日短信。[kickoff](https://www.trainwithkickoff.com/)
- Ladder：$29.99/月，教练编排课程（非 1:1）；5 万付费会员 → $105M B 轮 + $90M 增长投资。[Pulse2](https://pulse2.com/ladder-12-million-funding/)｜[SiliconHills](https://www.siliconhillsnews.com/2024/11/23/ladder-secures-105-million-to-expand-global-reach-and-revolutionize-strength-training/)
- CoPilot：$99/月真人教练；$6.5M A-1；150 万次训练记录。[Athletech](https://athletechnews.com/copilot-raises-another-6-5m-for-remote-personal-training-app/)

前四家是**自营双边**（平台招募/匹配教练、向用户收订阅）；Ladder 是教练内容订阅、非 1:1 匹配。教练侧则是**纯工具 SaaS**（不抽佣、按客户数订阅）：Trainerize $10–275/月（40 万教练、4.5 万商家）、TrueCoach $26.34–136.99/月（2 万+教练）、Everfit $19/月起、Practice Better $25–145/月（1.8 万从业者，累计融资 $40M）。[Trainerize](https://www.trainerize.com/pricing/)｜[TrueCoach](https://www.truecoach.co/pricing/)｜[Everfit](https://everfit.io/pricing/)｜[BetaKit](https://betakit.com/practice-better-looks-to-opportunistic-growth-with-18-million-cad-credit-facility-from-cibc/)

**2) 记录 App**
Hevy 1700 万用户、$2.99/月；Strong $4.99/月；Jefit 1200 万会员、$12.99/月；Fitbod $15.99/月。[Hevy](https://www.hevyapp.com/compare/fitbod/)｜[Jefit](https://www.jefit.com/elite/)
天花板：健康健身类付费者 LTV 中位 $16.44、每次安装收入 $0.44；硬付费墙下载转化 12.1% vs 免费增值 2.2%。纯工具无人工服务，价格锚定 $3–16/月。[RevenueCat](https://www.revenuecat.com/state-of-subscription-apps-2025/)

**3) AI 教练**
Whoop Coach（GPT-4，2023/9）含在会员内不另收费；Fitbit Coach（Gemini）2025/10 起 Premium 公测；Apple Project Mulberry 2026 年初被曝缩减、未上线。[Athletech](https://athletechnews.com/whoop-launches-ai-health-fitness-coach-using-chatgpt-tech/)｜[TechCrunch](https://techcrunch.com/2025/10/27/fitbits-revamped-app-with-gemini-powered-health-coach-rolls-out-to-premium-users/)｜[Mashable](https://in.mashable.com/tech/105498/apple-faces-ai-health-competition-scales-back-virtual-coach-plans-report)
质量：24 项研究/2512 人系统综述——6 项人机头对头 5 项 LLM 劣于人类；14/24（58%）有安全缺陷（给临床人群开禁忌动作）；结论「只能辅助，不能替代」。[Biology of Sport 2026](https://pmc.ncbi.nlm.nih.gov/articles/PMC13343266/)

**4) 姿态识别**
MediaPipe（Apache-2.0，33 点）、ML Kit（33 点）、Apple Vision（iOS17+，17 关节 3D）均端侧推理、无按次调用费。[MediaPipe](https://developers.google.com/edge/mediapipe/solutions/vision/pose_landmarker)｜[ML Kit](https://developers.google.com/ml-kit/vision/pose-detection/android)｜[Vision](https://developer.apple.com/documentation/vision/identifying-3d-human-body-poses-in-images)
精度：单目 MediaPipe 膝外翻角比 VICON 高 18.8–19.7°，绝对值无显著同时效度。[Heliyon 2024](https://pubmed.ncbi.nlm.nih.gov/39281584/)
商用：Kaia 2026/1 被 Sword Health 收购；Exer 主打临床 MSK（梅奥合作）；Uplift Labs 双 iPhone 动捕，1/3+ MLB 球队使用。全走 B 端/临床/职业体育——精度不足、遮挡、无负荷数据、责任风险，故只能做辅助。[Kaia](https://kaiahealth.com/newsroom/press-releases/kaia-health-joins-sword-health/)｜[SBJ](https://www.sportsbusinessjournal.com/Daily/Issues/2023/04/26/Technology/uplift-labs-tommy-john-surgery-risk-factors/)

**5) 判断**
定位「**真人教练的效率工具 + 低价自助入口**」，不是替代：LLM 计划质量与安全未达标；真人层 $99–200/月仍在增长，AI 被并入订阅不加价，SaaS 则把 AI 卖给教练（Trainerize「AI Workout Builder」）。「AI 免费层 + 真人付费层」**未查到跑通案例**；最接近 Caliber（免费记录→$19→$200 真人），但免费层不含 AI 教练。
