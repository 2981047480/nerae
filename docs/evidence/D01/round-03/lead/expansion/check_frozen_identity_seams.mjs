// Lead-only module QA, not browser or backend evidence. Run only after source freeze.
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { readFile } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

const revision=process.argv[2]
assert.match(revision??'',/^[a-f0-9]{40}$/,'Pass the actual frozen source revision')
const git=(...args)=>execFileSync('git',args,{encoding:'utf8'}).trim()
assert.equal(git('rev-parse','HEAD'),revision,'HEAD differs from requested freeze')
assert.equal(git('status','--porcelain','--','.'),'','Prototype source is not clean/frozen')
const tracked=execFileSync('git',['ls-files','-z','--','.'],{encoding:'utf8'}).split('\0').filter(Boolean)
const manifest=async()=>Object.fromEntries(await Promise.all(tracked.map(async p=>[p,createHash('sha256').update(await readFile(p)).digest('hex')])))
const sourceBefore=await manifest()
const timers=new Set(),intervals=new Set(),data=new Map()
let accepting=true
globalThis.window={
 scrollY:0,
 localStorage:{getItem:k=>data.get(k)??null,setItem:(k,v)=>data.set(k,v),removeItem:k=>data.delete(k)},
 setTimeout(fn,ms){if(!accepting)return 0;const id=setTimeout(()=>{timers.delete(id);fn()},ms);timers.add(id);return id},
 clearTimeout(id){clearTimeout(id);timers.delete(id)},
 setInterval(fn,ms){const id=setInterval(fn,ms);intervals.add(id);return id},
 clearInterval(id){clearInterval(id);intervals.delete(id)},
 addEventListener(){},scrollTo(){},
 matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){}}),
}
const {createServer}=await import(pathToFileURL(resolve('node_modules/vite/dist/node/index.js')).href)
const server=await createServer({root:process.cwd(),configFile:false,cacheDir:'/private/tmp/nerae-lead-frozen-identity',server:{middlewareMode:true,hmr:false,ws:false},appType:'custom'})
const report={scope:'actual frozen modules; fake window/storage; no browser/backend claim',revision,sourceBefore,checks:[],status:'partial'}
try {
 const s=await server.ssrLoadModule('/src/state/store.ts')
 const plain=x=>JSON.parse(JSON.stringify(x))
 async function check(name,fn) {
   s.resetPreview();const evidence={}
   try {await fn(evidence);report.checks.push({name,status:'passed',evidence})}
   catch(error){process.exitCode=1;report.checks.push({name,status:'failed',error:String(error?.stack??error),evidence})}
 }
 await check('independent student drafts survive switching',async e=>{
   const linBefore=plain(s.state.coach)
   s.goToCoachPlanForStudent('zhou-lan')
   const day=s.state.coach.draft.days[0],exercise=day.exercises[0]
   const note=exercise.note+'；本周调整'
   s.updatePlanField(day.id,exercise.id,'note',note)
   assert.equal(s.state.coach.draft.days[0].exercises[0].note,note,'Edit setup did not change Zhou note')
   e.editedZhouNote=note
   assert.equal(await s.saveDraft(),true,'Valid Zhou draft did not save')
   const zhouSaved=plain(s.state.coach)
   s.goToCoachPlanForStudent('lin-che')
   assert.deepEqual(plain(s.state.coach),linBefore,'Editing Zhou changed Lin')
   e.linUnchanged=true
   s.goToCoachPlanForStudent('zhou-lan')
   assert.deepEqual(plain(s.state.coach),zhouSaved,'Zhou saved draft lost after switch')
   e.zhouSavedNote=s.state.coach.draft.days[0].exercises[0].note
 })
 await check('Lin adoption while last coach context was Zhou',async e=>{
   s.goToCoachPlanForStudent('zhou-lan')
   const zhouBefore=plain(s.state.coach)
   s.jumpToScenario('today')
   s.openAdoptSheet()
   e.adopted=s.confirmAdoption()
   assert.equal(e.adopted,true,'Valid Lin incoming plan not adopted')
   e.linStudentVersion=s.state.student.planVersion
   assert.equal(e.linStudentVersion,7)
   s.goToCoachPlanForStudent('zhou-lan')
   e.zhouVersion=s.state.coach.activeStudentVersion
   assert.deepEqual(plain(s.state.coach),zhouBefore,'Lin adoption changed Zhou coach state')
   s.goToCoachPlanForStudent('lin-che')
   e.linCoachVersion=s.state.coach.activeStudentVersion
   assert.equal(e.linCoachVersion,7,'Lin coach context did not reflect Lin adoption')
 })
 await check('Zhou publication leaves Lin delivery and messages unchanged',async e=>{
   const linIncoming=plain(s.state.student.incoming)
   const linMessages=plain(s.state.conversations.find(x=>x.id==='conv-gu-yan')?.messages??[])
   const linCoach=plain(s.state.coach)
   s.goToCoachPlanForStudent('zhou-lan')
   const day=s.state.coach.draft.days[0],exercise=day.exercises[0]
   const note=exercise.note+'；交付调整'
   s.updatePlanField(day.id,exercise.id,'note',note)
   assert.equal(s.state.coach.draft.days[0].exercises[0].note,note,'Publication edit setup did not change note')
   assert.equal(await s.saveDraft(),true)
   s.requestPublish()
   e.sheet=s.ui.sheet
   assert.equal(e.sheet,'publish','Valid Zhou plan did not reach publication confirmation')
   e.published=s.confirmPublish()
   assert.equal(e.published,true)
   const latest=[...s.state.coach.deliveries].sort((a,b)=>b.version-a.version)[0]
   e.zhouDeliveryVersion=latest?.version;e.zhouDeliveryNote=latest?.content.days[0]?.exercises[0]?.note
   assert.equal(e.zhouDeliveryNote,note,'Zhou publication did not deliver actual edit')
   assert.deepEqual(plain(s.state.student.incoming),linIncoming,'Zhou publication overwrote Lin incoming')
   assert.deepEqual(plain(s.state.conversations.find(x=>x.id==='conv-gu-yan')?.messages??[]),linMessages,'Zhou publication sent Lin a message')
   s.goToCoachPlanForStudent('lin-che')
   assert.deepEqual(plain(s.state.coach),linCoach,'Zhou publication changed Lin coach plan')
 })
 await check('active Zhou reply opens Zhou rather than Lin',async e=>{
   s.goToCoachStudentDetail('zhou-lan')
   s.replyToStudent('zhou-lan')
   e.screen=s.state.screen;e.conversationId=s.ui.activeConversationId;e.note=s.ui.saveNote
   assert.equal(e.screen,'messages','Active Zhou reply did not open a conversation')
   assert.ok(e.conversationId&&e.conversationId!=='conv-gu-yan','Zhou reply used Lin conversation')
   const zhouConversationId=e.conversationId
   s.jumpToScenario('today');s.goToMessages()
   assert.ok(!s.conversationList.value.some(x=>x.id===zhouConversationId),'Student Lin was given Zhou conversation')
 })
 await check('revocation reaches coach evidence while ordinary replies remain available',async e=>{
   const ownHealthText=s.state.conversations.find(x=>x.id==='conv-gu-yan').messages.find(x=>x.health&&x.author==='student').text
   s.confirmHealthRevoke()
   s.goToCoachStudentDetail('lin-che')
   e.visibleEvidence=plain(s.selectedStudentEvidence.value)
   assert.ok(e.visibleEvidence.every(x=>x.kind==='plan'),'Revoked training/feedback/diet evidence remained in coach selector')
   s.jumpToScenario('today');s.goToMessages();s.openConversation('conv-gu-yan')
   assert.ok(s.currentConversation.value.messages.some(x=>x.text===ownHealthText),'Student lost their own health message after revocation')
   s.goToCoachStudentDetail('lin-che')
   s.replyToStudent('lin-che')
   assert.equal(s.state.screen,'messages')
   assert.equal(s.ui.activeConversationId,'conv-gu-yan')
   s.setMessageDraft('conv-gu-yan','周六的安排可以晚半小时吗')
   e.ordinarySent=s.sendMessage('conv-gu-yan')
   assert.equal(e.ordinarySent,true,'Health revocation incorrectly blocked ordinary arrangement reply')
 })
 await check('new messages update the recipient unread cursor independently',async e=>{
   s.jumpToScenario('today');s.goToMessages();s.openConversation('conv-gu-yan')
   s.setMessageDraft('conv-gu-yan','周六的训练可以晚半小时开始吗')
   assert.equal(s.sendMessage('conv-gu-yan'),true)
   e.studentUnreadAfterOwnSend=s.conversationList.value.find(x=>x.id==='conv-gu-yan')?.unread
   assert.equal(e.studentUnreadAfterOwnSend,0,'Sender acquired unread from own message')
   s.goToCoachTodo();s.goToMessages()
   e.coachUnreadBeforeOpen=s.conversationList.value.find(x=>x.id==='conv-gu-yan')?.unread
   assert.ok(e.coachUnreadBeforeOpen>=1,'Student message did not become unread for coach')
   s.openConversation('conv-gu-yan')
   s.setMessageDraft('conv-gu-yan','可以，按调整后的时间开始。')
   assert.equal(s.sendMessage('conv-gu-yan'),true)
   s.jumpToScenario('today');s.goToMessages()
   e.studentUnreadBeforeOpen=s.conversationList.value.find(x=>x.id==='conv-gu-yan')?.unread
   assert.ok(e.studentUnreadBeforeOpen>=1,'Coach message did not become unread for student')
 })
 await check('self-only observation stays private despite a general health grant',async e=>{
   s.goToCoachStudentDetail('lin-che')
   e.visibleEvidence=plain(s.selectedStudentEvidence.value)
   assert.ok(!e.visibleEvidence.some(x=>x.id==='lin-3'||x.summary?.includes('晚餐蔬菜偏少')),'Explicitly self-only observation was exposed by general health authorization')
 })
 await check('ended relationship removes coach health reads and blocks new sends',async e=>{
   const raw=s.state.conversations.find(x=>x.id==='conv-gu-yan')
   const sensitiveTexts=raw.messages.filter(x=>x.health||x.refs?.some(ref=>ref.kind==='training')).map(x=>x.text).filter(Boolean)
   s.applyPreviewState('messages','readonly')
   s.goToCoachStudentDetail('lin-che')
   e.visibleEvidence=plain(s.selectedStudentEvidence.value)
   assert.ok(e.visibleEvidence.every(x=>x.kind==='plan'),'Ended relationship retained coach health evidence')
   s.replyToStudent('lin-che')
   e.readOnly=s.messagesReadOnly.value
   assert.equal(e.readOnly,true)
   const projected=s.currentConversation.value?.messages??[]
   assert.ok(!projected.some(m=>sensitiveTexts.some(text=>m.text?.includes(text))),'Ended relationship retained sensitive message text in coach projection')
   s.setMessageDraft('conv-gu-yan','服务结束后不应发送')
   assert.equal(s.sendMessage('conv-gu-yan'),false,'Ended relationship sent a new message')
 })
 await check('failed send is not received and same-ID retry counts once',async e=>{
   s.goToCoachTodo();s.goToMessages();s.openConversation('conv-gu-yan')
   s.jumpToScenario('today');s.goToMessages();s.openConversation('conv-gu-yan')
   s.applyPreviewState('messages','failed')
   s.setMessageDraft('conv-gu-yan','失败重试只接收一次')
   assert.equal(s.sendMessage('conv-gu-yan'),true)
   const id=s.ui.lastSentMessageId
   const raw=s.state.conversations.find(x=>x.id==='conv-gu-yan')
   assert.equal(raw.messages.find(x=>x.id===id)?.status,'failed')
   s.goToCoachTodo();s.goToMessages()
   e.failedUnread=s.conversationList.value.find(x=>x.id==='conv-gu-yan')?.unread
   assert.equal(e.failedUnread,0,'Failed message was counted as received')
   s.openConversation('conv-gu-yan')
   assert.ok(!s.currentConversation.value.messages.some(x=>x.id===id),'Recipient received a failed outgoing message')
   s.jumpToScenario('today');s.goToMessages();s.openConversation('conv-gu-yan')
   assert.equal(s.retryMessage(id,'conv-gu-yan'),true)
   s.retryMessage(id,'conv-gu-yan')
   assert.equal(raw.messages.filter(x=>x.id===id).length,1,'Retry duplicated the stable message ID')
   s.goToCoachTodo();s.goToMessages()
   e.afterRetryUnread=s.conversationList.value.find(x=>x.id==='conv-gu-yan')?.unread
   assert.equal(e.afterRetryUnread,1,'Same-ID retry was not received exactly once')
 })
 report.status=report.checks.every(x=>x.status==='passed')?'passed':'failed'
} catch(error) {report.status='failed';report.error=String(error?.stack??error);process.exitCode=1}
finally {
 accepting=false;for(const id of timers)clearTimeout(id);for(const id of intervals)clearInterval(id);await server.close()
 report.sourceAfter=await manifest()
 report.sourceStable=JSON.stringify(sourceBefore)===JSON.stringify(report.sourceAfter)
 if(!report.sourceStable){report.status='failed';process.exitCode=1}
 console.log(JSON.stringify(report,null,2))
}
