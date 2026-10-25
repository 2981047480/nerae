// Lead-only module QA, not browser or backend evidence. Run only after source freeze.
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { readFile, writeFile } from 'node:fs/promises'
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
 async function coldCurrent(tag) {
   assert.equal(s.retryPersist(),true,'Current selection failed to persist')
   await Promise.resolve()
   for(const id of timers)clearTimeout(id)
   timers.clear()
   const cold=await server.ssrLoadModule(`/src/state/store.ts?lead-reference-cold=${tag}`)
   assert.notEqual(cold.state,s.state,'Cold reference test reused the original module')
   return cold
 }
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
 await check('historical Lin reference survives last coach context being Zhou',async e=>{
   const original=plain(s.state.student.incoming)
   const conversation=s.state.conversations.find(x=>x.id==='conv-gu-yan')
   const oldRef=plain(conversation.messages.flatMap(x=>x.refs??[]).find(x=>x.kind==='plan'))
   assert.ok(oldRef,'Seed plan message reference missing')
   s.goToCoachPlanForStudent('lin-che')
   const day=s.state.coach.draft.days[0],exercise=day.exercises[0]
   s.updatePlanField(day.id,exercise.id,'note','新版本专用备注，不能出现在旧引用中')
   assert.equal(await s.saveDraft(),true)
   s.requestPublish();assert.equal(s.ui.sheet,'publish')
   assert.equal(s.confirmPublish(),true)
   assert.ok(s.state.student.incoming.version>original.version)
   e.latestVersion=s.state.student.incoming.version
   s.goToCoachPlanForStudent('zhou-lan')
   const zhouBefore=plain(s.state.coach)
   s.jumpToScenario('today');s.goToConversation('conv-gu-yan');s.openMessageRef(oldRef)
   const shown=s.incomingDelivery.value
   e.referenceId=oldRef.id;e.shownVersion=shown?.version
   assert.equal(shown?.version,original.version,'Old Lin reference fell back to latest or another student')
   assert.deepEqual(plain(shown.content),original.content,'Historical delivery content was rewritten')
   assert.equal(s.incomingAvailable.value,false,'Replaced historical version was offered for adoption')
   s.openAdoptSheet()
   assert.notEqual(s.ui.sheet,'adopt','Old reference opened adoption of a different latest version')
   s.goToCoachPlanForStudent('zhou-lan')
   assert.deepEqual(plain(s.state.coach),zhouBefore,'Reading Lin history changed Zhou plan state')
 })
 await check('coach plan reference preserves Zhou participant and read-only role',async e=>{
   const linBefore=plain(s.state.student)
   s.goToCoachPlanForStudent('zhou-lan')
   const day=s.state.coach.draft.days[0],exercise=day.exercises[0]
   s.updatePlanField(day.id,exercise.id,'note',exercise.note+'；本次交付给周岚')
   assert.equal(await s.saveDraft(),true)
   s.requestPublish();assert.equal(s.confirmPublish(),true)
   const conversation=s.state.conversations.find(x=>x.studentId==='zhou-lan')
   const ref=plain([...conversation.messages].reverse().flatMap(x=>x.refs??[]).find(x=>x.kind==='plan'))
   assert.ok(ref,'Zhou publication did not produce a reference')
   s.replyToStudent('zhou-lan');s.openMessageRef(ref)
   e.actor=s.state.actor;e.screen=s.state.screen;e.selectedStudent=s.ui.selectedStudentId
   assert.equal(s.state.actor,'coach','Coach reference changed into student Lin identity')
   assert.equal(s.ui.selectedStudentId,'zhou-lan','Coach reference selected a different student')
   if(s.state.screen==='incomingPlan'){
     assert.equal(s.incomingDelivery.value?.content.title,s.state.coach.draft.title,'Zhou reference displayed Lin content')
     assert.equal(s.incomingAvailable.value,false,'Coach reference offered student adoption')
   }
   assert.deepEqual(plain(s.state.student),linBefore,'Reading Zhou reference changed Lin student state')
 })
 await check('missing plan reference never silently displays latest delivery',async e=>{
   s.jumpToScenario('today');s.goToConversation('conv-gu-yan')
   const latest=s.state.student.incoming.version
   s.openMessageRef({kind:'plan',id:'delivery-999999',title:'不存在的交付',detail:'QA negative reference'})
   e.screen=s.state.screen;e.shownVersion=s.incomingDelivery.value?.version??null;e.note=s.ui.saveNote
   assert.ok(!(e.screen==='incomingPlan'&&e.shownVersion===latest),'Missing reference was silently replaced with latest content')
   assert.equal(s.state.student.incoming.version,latest,'Missing reference changed current incoming data')
 })
 await check('published draft work follows add remove and undo without false pending work',async e=>{
   s.goToCoachPlanForStudent('zhou-lan')
   const day=s.state.coach.draft.days[0],exercise=day.exercises[0]
   s.updatePlanField(day.id,exercise.id,'note',exercise.note+'；工作项生命周期检查')
   assert.equal(await s.saveDraft(),true)
   s.requestPublish();assert.equal(s.confirmPublish(),true)
   const todo=s.state.todoItems.find(x=>x.studentId==='zhou-lan'&&x.reason==='draft')
   assert.ok(todo,'Zhou draft work item missing')
   assert.equal(todo.done,true,'Published draft remained pending')
   s.removeExercise(day.id,exercise.id)
   assert.equal(todo.done,false,'Removing an exercise did not reopen draft work')
   s.undoRemoveExercise()
   assert.equal(todo.done,true,'Undo back to published content left a false unsaved task')
   const beforeIds=new Set(s.state.coach.draft.days[0].exercises.map(x=>x.id))
   s.addExercise(day.id)
   assert.equal(todo.done,false,'Adding an exercise did not reopen draft work')
   const added=s.state.coach.draft.days[0].exercises.find(x=>!beforeIds.has(x.id))
   assert.ok(added,'Add exercise setup did not create an exercise')
   s.removeExercise(day.id,added.id)
   assert.equal(todo.done,true,'Removing the just-added row did not restore completed draft work')
   e.currentDraftMatchesDelivered=true;e.finalDraftTodoDone=todo.done
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
   const ownMessage=s.currentConversation.value.messages.find(x=>x.text===ownHealthText)
   assert.equal(s.messageTextForActor(s.currentConversation.value,ownMessage),ownHealthText,'Owner display projection hid their own retained message')
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
   e.studentUnreadAfterOwnSend=s.conversationUnread(s.conversationList.value.find(x=>x.id==='conv-gu-yan'))
   assert.equal(e.studentUnreadAfterOwnSend,0,'Sender acquired unread from own message')
   s.goToCoachTodo();s.goToMessages()
   e.coachUnreadBeforeOpen=s.conversationUnread(s.conversationList.value.find(x=>x.id==='conv-gu-yan'))
   assert.ok(e.coachUnreadBeforeOpen>=1,'Student message did not become unread for coach')
   s.openConversation('conv-gu-yan')
   s.setMessageDraft('conv-gu-yan','可以，按调整后的时间开始。')
   assert.equal(s.sendMessage('conv-gu-yan'),true)
   s.jumpToScenario('today');s.goToMessages()
   e.studentUnreadBeforeOpen=s.conversationUnread(s.conversationList.value.find(x=>x.id==='conv-gu-yan'))
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
   const conversation=s.currentConversation.value
   const projected=(conversation?.messages??[]).map(m=>s.messageTextForActor(conversation,m))
   assert.ok(!projected.some(body=>sensitiveTexts.some(text=>body?.includes(text))),'Ended relationship retained sensitive message text in coach display projection')
   assert.ok(conversation.messages.every(m=>!s.messageRefsForActor(conversation,m).some(ref=>ref.kind==='training')),'Ended relationship retained a training reference in coach display projection')
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
   e.failedUnread=s.conversationUnread(s.conversationList.value.find(x=>x.id==='conv-gu-yan'))
   assert.equal(e.failedUnread,0,'Failed message was counted as received')
   s.openConversation('conv-gu-yan')
   // Raw shared storage is deliberately retained for the sender. Recipient DOM
   // visibility is checked separately through the actual browser after freeze.
   e.recipientRetry=s.retryMessage(id,'conv-gu-yan')
   assert.equal(e.recipientRetry,false,'Recipient was allowed to retry sender failure')
   s.discardFailedMessage(id,'conv-gu-yan')
   assert.equal(raw.messages.find(x=>x.id===id)?.status,'failed','Recipient deleted or changed sender failure')
   s.jumpToScenario('today');s.goToMessages();s.openConversation('conv-gu-yan')
   assert.equal(s.retryMessage(id,'conv-gu-yan'),true)
   s.retryMessage(id,'conv-gu-yan')
   assert.equal(raw.messages.filter(x=>x.id===id).length,1,'Retry duplicated the stable message ID')
   s.goToCoachTodo();s.goToMessages()
   e.afterRetryUnread=s.conversationUnread(s.conversationList.value.find(x=>x.id==='conv-gu-yan'))
   assert.equal(e.afterRetryUnread,1,'Same-ID retry was not received exactly once')
 })
 await check('legacy draft authorship is not inferred from last navigation actor',async e=>{
   const coldFrom=async(raw,tag)=>{
     // Flush/reset the fake-store persistence timer before replacing its test
     // storage. This is a module migration check, not browser-state injection.
     await Promise.resolve()
     for(const id of timers)clearTimeout(id)
     timers.clear()
     data.set(s.storageKey,JSON.stringify(raw))
     return await server.ssrLoadModule(`/src/state/store.ts?lead-legacy-owner=${tag}`)
   }
   const raw=plain(s.state)
   const conversation=raw.conversations.find(x=>x.id==='conv-gu-yan')
   delete conversation.drafts
   delete conversation.unassignedLegacyDraft
   delete conversation.draftOwnerInferred
   conversation.draft='作者未记录的旧安排草稿'
   raw.actor='coach'
   const cold=await coldFrom(raw,'missing-owner')
   assert.notEqual(cold.state,s.state,'Migration fixture did not load a distinct cold module')
   const migrated=cold.state.conversations.find(x=>x.id==='conv-gu-yan')
   assert.equal(migrated.drafts.student,'','Unknown legacy draft appeared in student composer source')
   assert.equal(migrated.drafts.coach,'','Last navigation actor was mistaken for draft author')
   assert.equal(migrated.unassignedLegacyDraft,conversation.draft,'Unknown legacy text was lost')
   e.unassignedRetained=true

   const inferredRaw=plain(s.state)
   const inferred=inferredRaw.conversations.find(x=>x.id==='conv-gu-yan')
   inferred.drafts={student:'已知属于学员的新草稿',coach:'曾被错误推断给教练的旧草稿'}
   inferred.draftOwnerInferred='coach'
   inferredRaw.actor='coach'
   const coldInferred=await coldFrom(inferredRaw,'inferred-owner')
   assert.notEqual(coldInferred.state,s.state,'Inferred-owner fixture did not cold-load')
   const repaired=coldInferred.state.conversations.find(x=>x.id==='conv-gu-yan')
   assert.equal(repaired.drafts.student,'已知属于学员的新草稿','Repair erased a known participant draft')
   assert.equal(repaired.drafts.coach,'','Unconfirmed inferred owner remained authoritative')
   assert.equal(repaired.unassignedLegacyDraft,'曾被错误推断给教练的旧草稿','Repair dropped the inferred legacy text')
   e.knownDraftPreserved=true
 })
 await check('coach reference cold reload and back retain reader student and read-only scope',async e=>{
   const linBefore=plain(s.state.student)
   s.goToCoachPlanForStudent('zhou-lan')
   assert.equal(await s.saveDraft(),true)
   s.requestPublish();assert.equal(s.ui.sheet,'publish')
   assert.equal(s.confirmPublish(),true)
   const delivery=[...s.state.coach.deliveries].sort((a,b)=>b.version-a.version)[0]
   assert.ok(delivery)
   s.replyToStudent('zhou-lan')
   const ref=plain([...s.state.conversations.find(x=>x.studentId==='zhou-lan').messages].reverse().flatMap(x=>x.refs??[]).find(x=>x.kind==='plan'))
   assert.ok(ref,'Published message reference fixture missing')
   s.openMessageRef(ref)
   const cold=await coldCurrent('coach-zhou')
   e.actor=cold.state.actor;e.studentId=cold.ui.incomingHistoryStudentId;e.version=cold.incomingDelivery.value?.version
   assert.equal(cold.state.actor,'coach')
   assert.equal(cold.ui.incomingHistoryStudentId,'zhou-lan')
   assert.equal(cold.ui.incomingReadOnly,true)
   assert.equal(cold.incomingDelivery.value?.version,delivery.version)
   assert.equal(cold.incomingAvailable.value,false)
   cold.openAdoptSheet();assert.notEqual(cold.ui.sheet,'adopt')
   assert.deepEqual(plain(cold.state.student),linBefore,'Reading Zhou after cold reload changed Lin')
   cold.goBack()
   assert.equal(cold.state.actor,'coach','Back from shared reference switched to student')
   assert.equal(cold.state.screen,'messages')
   assert.equal(cold.ui.activeConversationId,'conv-zhou-lan')
 })
 await check('student old reference cold reload does not become latest after coach context changed',async e=>{
   const original=plain(s.state.student.incoming)
   const ref=plain(s.state.conversations.find(x=>x.id==='conv-gu-yan').messages.flatMap(x=>x.refs??[]).find(x=>x.kind==='plan'))
   assert.ok(ref)
   s.goToCoachPlanForStudent('lin-che')
   const day=s.state.coach.draft.days[0],exercise=day.exercises[0]
   s.updatePlanField(day.id,exercise.id,'note','本周先保持动作稳定，再增加次数。')
   assert.equal(await s.saveDraft(),true)
   s.requestPublish();assert.equal(s.ui.sheet,'publish');assert.equal(s.confirmPublish(),true)
   assert.ok(s.state.student.incoming.version>original.version)
   s.goToCoachPlanForStudent('zhou-lan');s.jumpToScenario('today');s.goToConversation('conv-gu-yan');s.openMessageRef(ref)
   const cold=await coldCurrent('student-history')
   e.actor=cold.state.actor;e.version=cold.incomingDelivery.value?.version
   assert.equal(cold.state.actor,'student')
   assert.equal(cold.ui.incomingHistoryStudentId,'lin-che')
   assert.equal(cold.incomingDelivery.value?.version,original.version)
   assert.deepEqual(plain(cold.incomingDelivery.value.content),original.content)
   assert.equal(cold.incomingAvailable.value,false)
   cold.goBack();assert.equal(cold.state.actor,'student');assert.equal(cold.state.screen,'messages')
 })
 await check('missing reference remains unavailable through cold reload and adoption guard',async e=>{
   s.goToConversation('conv-gu-yan')
   s.openMessageRef({kind:'plan',id:'delivery-999999',title:'不存在的交付',detail:'缺失引用不能替换为最新'})
   const cold=await coldCurrent('missing')
   e.missing=cold.ui.incomingRefMissing;e.shown=cold.incomingDelivery.value?.version??null
   assert.equal(cold.ui.incomingRefMissing,true)
   assert.equal(cold.incomingDelivery.value==null,true)
   assert.equal(cold.incomingAvailable.value,false)
   cold.openAdoptSheet();assert.notEqual(cold.ui.sheet,'adopt')
 })
 await check('non-default training reference retains exact record across cold reload and back',async e=>{
   const record=s.state.records.find(x=>x.id==='record-20260929')
   assert.ok(record,'Non-default record fixture missing')
   s.goToConversation('conv-gu-yan')
   s.openMessageRef({kind:'training',id:record.id,title:'9月29日训练',detail:'原记录'})
   const cold=await coldCurrent('record-september')
   e.recordId=cold.ui.recordsFocusId;e.explicit=cold.ui.recordsFocusExplicit
   assert.equal(cold.state.screen,'records')
   assert.equal(cold.ui.recordsFocusExplicit,true)
   assert.equal(cold.ui.recordsFocusId,record.id,'Cold reload fell back to default October22 record')
   cold.goBack();assert.equal(cold.state.screen,'messages');assert.equal(cold.state.actor,'student')
 })
 await check('legacy shared messages cache keeps its actual coach reader',async e=>{
   s.goToCoachPlanForStudent('zhou-lan');s.replyToStudent('zhou-lan');s.retryPersist()
   const raw=JSON.parse(data.get(s.storageKey))
   delete raw.selection.actor
   await Promise.resolve();for(const id of timers)clearTimeout(id);timers.clear()
   data.set(s.storageKey,JSON.stringify(raw))
   const cold=await server.ssrLoadModule('/src/state/store.ts?lead-legacy-shared=coach-message')
   e.savedActor=raw.actor;e.actorAfter=cold.state.actor;e.conversation=cold.ui.activeConversationId
   assert.equal(cold.state.actor,'coach','A valid old coach messages cache defaulted to student')
   assert.equal(cold.ui.activeConversationId,'conv-zhou-lan')
 })
 await check('legacy reference without persisted owner version is unavailable rather than Lin latest',async e=>{
   s.goToCoachPlanForStudent('zhou-lan');s.replyToStudent('zhou-lan');s.retryPersist()
   const raw=JSON.parse(data.get(s.storageKey));raw.screen='incomingPlan';raw.stack=['messages']
   for(const field of ['actor','incomingHistoryVersion','incomingHistoryStudentId','incomingReadOnly','incomingRefMissing'])delete raw.selection[field]
   await Promise.resolve();for(const id of timers)clearTimeout(id);timers.clear()
   data.set(s.storageKey,JSON.stringify(raw))
   const cold=await server.ssrLoadModule('/src/state/store.ts?lead-legacy-shared=unknown-reference')
   e.actorAfter=cold.state.actor;e.shownVersion=cold.incomingDelivery.value?.version??null;e.available=cold.incomingAvailable.value
   assert.equal(cold.state.actor,'coach','Legacy reader changed while restoring unknown reference')
   assert.equal(cold.incomingDelivery.value==null,true,'Unknown old reference silently became Lin latest')
   assert.equal(cold.incomingAvailable.value,false)
   cold.openAdoptSheet();assert.notEqual(cold.ui.sheet,'adopt')
 })
 await check('legacy training reference cannot silently expand default October22 on cold reload',async e=>{
   s.goToConversation('conv-gu-yan');s.retryPersist()
   const raw=JSON.parse(data.get(s.storageKey));raw.screen='records';raw.stack=['messages']
   delete raw.selection.recordsFocusId;delete raw.selection.recordsFocusExplicit
   await Promise.resolve();for(const id of timers)clearTimeout(id);timers.clear()
   data.set(s.storageKey,JSON.stringify(raw))
   const cold=await server.ssrLoadModule('/src/state/store.ts?lead-legacy-shared=unknown-training')
   e.explicit=cold.ui.recordsFocusExplicit;e.recordId=cold.ui.recordsFocusId
   assert.equal(cold.ui.recordsFocusExplicit,true,'Unknown legacy training reference fell back to default record mode')
   assert.equal(cold.ui.recordsFocusId,null,'Unknown record identity was guessed')
 })
 await check('student own latest message reference remains eligible for explicit adoption',async e=>{
   const beforeSession=plain(s.state.session)
   s.jumpToScenario('today');s.goToConversation('conv-gu-yan')
   const ref=plain(s.state.conversations.find(x=>x.id==='conv-gu-yan').messages.flatMap(x=>x.refs??[])
     .find(x=>x.kind==='plan'&&x.id===`delivery-${s.state.student.incoming.version}`))
   assert.ok(ref,'Seed latest message reference missing')
   s.openMessageRef(ref)
   e.version=s.incomingDelivery.value?.version;e.available=s.incomingAvailable.value
   assert.equal(e.version,7,'Latest reference changed the requested version')
   assert.equal(e.available,true,'Own latest actionable delivery was incorrectly treated as an unavailable snapshot')
   s.openAdoptSheet();assert.equal(s.ui.sheet,'adopt')
   s.closeSheet();assert.equal(s.state.student.planVersion,6,'Cancel adopted without confirmation')
   s.openAdoptSheet();assert.equal(s.confirmAdoption(),true)
   assert.equal(s.state.student.planVersion,7)
   assert.deepEqual(plain(s.state.session),beforeSession,'Adoption rewrote today confirmed session')
   e.adoptedAfterExplicitConfirmation=true;e.todayPreserved=true
 })
 await check('already adopted latest message reference keeps its actual status and cannot apply twice',async e=>{
   s.jumpToScenario('today');s.goToIncomingPlan();s.openAdoptSheet()
   assert.equal(s.confirmAdoption(),true,'Direct latest control did not adopt')
   const beforeSchedule=plain(s.state.student.schedule)
   s.goToConversation('conv-gu-yan')
   const ref=plain(s.state.conversations.find(x=>x.id==='conv-gu-yan').messages.flatMap(x=>x.refs??[])
     .find(x=>x.kind==='plan'&&x.id==='delivery-7'))
   s.openMessageRef(ref)
   e.shownStatus=s.incomingDelivery.value?.status
   assert.equal(e.shownStatus,'adopted','Latest reference falsely became waiting/new after adoption')
   assert.equal(s.confirmAdoption(),false,'Already adopted version was applied twice')
   assert.deepEqual(plain(s.state.student.schedule),beforeSchedule,'Duplicate confirmation changed future schedule')
   e.duplicateRejected=true
 })
 const doseFormat=await server.ssrLoadModule('/src/utils/format.ts')
 const doseTraining=await server.ssrLoadModule('/src/utils/training.ts')
 function startDosePlan(id) {
   s.goToPlanDetail(id)
   const result=s.requestStartPlan(id)
   if(result==='replace')s.confirmReplaceAndStart()
   assert.equal(s.state.screen,'training','Dose fixture did not enter training')
 }
 function reachCoreExercise(name) {
   startDosePlan('plan-core')
   for(let guard=0;s.currentEntry.value?.exercise.name!==name&&guard<8;guard++) {
     const entry=s.currentEntry.value
     assert.ok(entry,'Core fixture ended before requested exercise')
     if(entry.exercise.name==='死虫式')s.setInputValue(entry.set.id,'seconds','40')
     else if(entry.exercise.name==='鸟狗式')s.setInputValue(entry.set.id,'reps','10')
     else throw Error('Unexpected fixture exercise before '+name+': '+entry.exercise.name)
     assert.equal(s.completeCurrentSet(),true,'Valid preceding bodyweight dose was blocked: '+JSON.stringify(s.ui.trainingErrors))
     s.skipRest()
   }
   assert.equal(s.currentEntry.value?.exercise.name,name)
   return s.currentEntry.value
 }
 function finishDoseSession(values) {
   for(let guard=0;s.currentEntry.value&&guard<24;guard++) {
     s.skipRest()
     const entry=s.currentEntry.value
     if(!entry)break
     const actual=values[entry.exercise.name]
     assert.ok(actual,'No explicit QA dose for '+entry.exercise.name)
     for(const [field,value] of Object.entries(actual))s.setInputValue(entry.set.id,field,String(value))
     assert.equal(s.completeCurrentSet(),true,'Valid dose was blocked: '+entry.exercise.name)
   }
   assert.equal(s.isComplete.value,true,'Dose session did not finish within bounded actual confirmations')
 }
 function assertDoseChart(name,value,unit) {
   s.goToRecords();s.setRecordsExercise(name)
   const point=s.recordChart.value.find(x=>x.recordId===s.state.session.id)
   assert.ok(point,'Completed dose session is missing from records chart: '+name)
   assert.equal(point.value,value,'Chart changed confirmed dose: '+name)
   assert.match(point.unit,unit,'Chart changed confirmed unit: '+name)
   return plain(point)
 }
 await check('bodyweight per-side records repetitions without invented load and survives cold restore',async e=>{
   const entry=reachCoreExercise('鸟狗式')
   const before=s.doneSets.value
   s.setInputValue(entry.set.id,'reps','0')
   assert.equal(s.completeCurrentSet(),false,'Zero repetitions were accepted')
   assert.equal(s.doneSets.value,before,'Invalid dose added a confirmed set')
   s.setInputValue(entry.set.id,'reps','10')
   assert.equal(s.completeCurrentSet(),true,'Valid self-weight count required a fake positive kg value')
   assert.equal(entry.set.weightKg,null,'Bodyweight confirmation invented an external load')
   assert.equal(entry.set.reps,10);assert.equal(entry.set.seconds,null)
   e.label=doseFormat.setValueLabel(entry.exercise.kind,entry.set)
   assert.match(e.label,/10/);assert.match(e.label,/侧/);assert.doesNotMatch(e.label,/kg|秒/)
   const cold=await coldCurrent('bodyweight-confirmed')
   const restored=cold.state.session.exercises.find(x=>x.name==='鸟狗式').sets[0]
   assert.equal(restored.reps,10);assert.equal(restored.weightKg,null)
   assert.equal(cold.doneSets.value,before+1)
   e.confirmedDelta=1;e.restoredWithoutLoad=true
 })
 await check('seconds per-side remains time rather than weighted repetitions',async e=>{
   const entry=reachCoreExercise('侧桥')
   assert.match(entry.exercise.targetText,/秒.*侧/)
   s.setInputValue(entry.set.id,'seconds','0')
   assert.equal(s.completeCurrentSet(),false,'Zero time was accepted')
   s.setInputValue(entry.set.id,'seconds','30')
   assert.equal(s.completeCurrentSet(),true,'Valid seconds per side was not accepted')
   assert.equal(entry.set.seconds,30);assert.equal(entry.set.reps,null);assert.equal(entry.set.weightKg,null)
   e.label=doseFormat.setValueLabel(entry.exercise.kind,entry.set)
   assert.match(e.label,/30/);assert.match(e.label,/秒/);assert.match(e.label,/侧/)
   assert.doesNotMatch(e.label,/kg|次/)
   finishDoseSession({'死虫式':{seconds:40},'鸟狗式':{reps:10},'侧桥':{seconds:30},'平板支撑':{seconds:45}})
   e.timeChart=assertDoseChart('侧桥',30,/^秒(?:\/侧)?$/)
   e.bodyweightChart=assertDoseChart('鸟狗式',10,/^次(?:\/侧)?$/)
   startDosePlan('plan-core')
   e.repeatedTimeReference=doseTraining.referenceText(s.state.session.exercises.find(x=>x.name==='侧桥'))
   assert.match(e.repeatedTimeReference,/30/);assert.match(e.repeatedTimeReference,/秒/)
   assert.match(e.repeatedTimeReference,/侧/,'Per-side context was lost from the last confirmed reference')
   const conflictEntry=reachCoreExercise('侧桥')
   s.applyPreviewState('training','conflict')
   e.remoteTimeConflict=s.activeConflict.value.remoteText
   assert.match(e.remoteTimeConflict,/秒/);assert.match(e.remoteTimeConflict,/侧/)
   const beforeConflict=s.doneSets.value
   assert.equal(s.resolveSetConflict('remote'),true)
   assert.equal(conflictEntry.set.seconds,42);assert.equal(conflictEntry.set.reps,null)
   assert.equal(conflictEntry.set.weightKg,null);assert.equal(s.doneSets.value,beforeConflict+1)
   assert.equal(s.resolveSetConflict('remote'),false,'Resolved time conflict added a second fact')
 })
 await check('breath count is retained from public content through confirmation',async e=>{
   startDosePlan('plan-mobility')
   const entry=s.currentEntry.value
   assert.equal(entry.exercise.name,'猫牛式')
   assert.match(entry.exercise.targetText,/8.*呼吸/,'Parsing discarded the published breath unit')
   s.setInputValue(entry.set.id,'reps','8')
   assert.equal(s.completeCurrentSet(),true,'Breath count required kg or seconds')
   assert.equal(entry.set.reps,8);assert.equal(entry.set.seconds,null);assert.equal(entry.set.weightKg,null)
   e.label=doseFormat.setValueLabel(entry.exercise.kind,entry.set)
   assert.match(e.label,/8/);assert.match(e.label,/呼吸/);assert.doesNotMatch(e.label,/kg|秒/)
   finishDoseSession({'猫牛式':{reps:8},'90/90 髋部转换':{reps:6},'跪姿肩屈':{reps:8},'腿后侧伸展':{seconds:30},'髋屈肌伸展':{seconds:30},'胸椎旋转':{reps:8}})
   e.breathChart=assertDoseChart('猫牛式',8,/^次呼吸$/)
   startDosePlan('plan-mobility')
   e.repeatedBreathReference=doseTraining.referenceText(s.currentEntry.value.exercise)
   assert.match(e.repeatedBreathReference,/8/);assert.match(e.repeatedBreathReference,/呼吸/)
   assert.doesNotMatch(e.repeatedBreathReference,/kg|秒/)
   const conflictEntry=s.currentEntry.value
   s.setInputValue(conflictEntry.set.id,'reps','8')
   s.applyPreviewState('training','conflict')
   e.breathConflict=plain(s.activeConflict.value)
   assert.match(e.breathConflict.localText,/呼吸/);assert.match(e.breathConflict.remoteText,/呼吸/)
   const beforeConflict=s.doneSets.value
   assert.equal(s.resolveSetConflict('remote'),true)
   assert.equal(conflictEntry.set.reps,8);assert.equal(conflictEntry.set.seconds,null)
   assert.equal(conflictEntry.set.weightKg,null);assert.equal(s.doneSets.value,beforeConflict+1)
   assert.equal(s.resolveSetConflict('remote'),false,'Resolved breath conflict added a second fact')
 })
 await check('new valid duration session is not blocked by prior weight validation errors',async e=>{
   const old=s.currentEntry.value
   s.setInputValue(old.set.id,'weightKg','-1')
   assert.equal(s.completeCurrentSet(),false)
   assert.ok(s.ui.trainingErrors.weightKg,'Invalid-weight setup did not produce a weight error')
   startDosePlan('plan-core')
   const entry=s.currentEntry.value
   assert.equal(entry.exercise.name,'死虫式')
   assert.deepEqual(plain(s.ui.trainingErrors),{},'Unrelated old validation crossed into new session')
   s.setInputValue(entry.set.id,'seconds','40')
   assert.equal(s.completeCurrentSet(),true)
   assert.equal(s.doneSets.value,1,'New session gained extra confirmed facts')
   e.validSeconds=entry.set.seconds;e.noStaleWeightError=true
 })
 await check('mixed legacy weight and new count records do not become a false single-unit trend',async e=>{
   const originalRecords=plain(s.state.records)
   startDosePlan('plan-core')
   finishDoseSession({'死虫式':{seconds:40},'鸟狗式':{reps:10},'侧桥':{seconds:30},'平板支撑':{seconds:45}})
   s.goToRecords();s.setRecordsExercise('鸟狗式')
   e.points=plain(s.recordChart.value);e.summary=plain(s.recordSummary.value)
   assert.ok(e.points.some(x=>x.value===10&&/^次(?:\/侧)?$/.test(x.unit)),'Confirmed count is unavailable')
   assert.ok(e.points.every(x=>/^次(?:\/侧)?$/.test(x.unit)),'Legacy kg and new repetitions were plotted as one comparable series')
   if(e.points.length===1)assert.equal(e.summary.delta,null,'One comparable point fabricated a trend')
   for(const old of originalRecords)assert.deepEqual(plain(s.state.records.find(x=>x.id===old.id)),old,'Chart normalization rewrote a confirmed historical record')
   e.originalFactsRetained=true
 })
 report.status=report.checks.every(x=>x.status==='passed')?'passed':'failed'
} catch(error) {report.status='failed';report.error=String(error?.stack??error);process.exitCode=1}
finally {
 accepting=false;for(const id of timers)clearTimeout(id);for(const id of intervals)clearInterval(id);await server.close()
 report.sourceAfter=await manifest()
 report.sourceStable=JSON.stringify(sourceBefore)===JSON.stringify(report.sourceAfter)
 if(!report.sourceStable){report.status='failed';process.exitCode=1}
 const serialized=JSON.stringify(report,null,2)
 if(process.argv[3])await writeFile(process.argv[3],serialized+'\n',{flag:'wx'})
 console.log(serialized)
}
