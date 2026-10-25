// One or two groups per CUA call, under the 60s observation budget. All entries are normal UI controls.
export const configs=[
 ['s01',/^今天 /],['s02',/^发现 /],['s03',/^计划详情 /],['s04',/^训练 \d/],
 ['s05',/^记录 /],['s06',/^教练 \d/],['s07',/^教练详情 /],['s09',/^收到计划 /],
 ['s10',/^我的计划 /],['s11',/^AI 整理 /],['s12',/^饮食自评 /],['s13',/^待办 /],
 ['s14-list',/^学员 \d/],
 ['s14-detail',/^学员 \d/,{afterScenario:async c=>{await c.tab.playwright.getByRole('button',{name:/^林 林澈 在读/}).click()}}],
 ['s15',/^计划审阅 /],['s16',/^服务 /],
 ['s08-student',null,{role:'学员 林澈',navigation:{landmark:'学员主导航',button:'消息'},afterScenario:async c=>{await c.tab.playwright.getByRole('button',{name:/^顾 顾言 /}).click()}}],
 ['s08-coach',null,{role:'教练 顾言',navigation:{landmark:'教练主导航',button:'消息'},afterScenario:async c=>{await c.tab.playwright.getByRole('button',{name:/^林 林澈 /}).click()}}],
]
