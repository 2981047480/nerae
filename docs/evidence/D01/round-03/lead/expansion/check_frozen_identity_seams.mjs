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
   e.initialZhouTitle=s.state.coach.draft.title
   const zhouTitle=e.initialZhouTitle+' · 本周调整'
   s.updatePlanField('title',zhouTitle)
   assert.equal(await s.saveDraft(),true,'Valid Zhou draft did not save')
   const zhouSaved=plain(s.state.coach)
   s.goToCoachPlanForStudent('lin-che')
   e.linAfter=plain(s.state.coach)
   assert.deepEqual(e.linAfter,linBefore,'Editing Zhou changed Lin')
   s.goToCoachPlanForStudent('zhou-lan')
   e.zhouAfter=plain(s.state.coach)
   assert.deepEqual(e.zhouAfter,zhouSaved,'Zhou saved draft lost after switch')
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
   s.updatePlanField('title',s.state.coach.draft.title+' · 交付调整')
   assert.equal(await s.saveDraft(),true)
   s.requestPublish()
   e.sheet=s.ui.sheet
   assert.equal(e.sheet,'publish','Valid Zhou plan did not reach publication confirmation')
   e.published=s.confirmPublish()
   assert.equal(e.published,true)
   e.zhouDeliveries=plain(s.state.coach.deliveries)
   assert.ok(e.zhouDeliveries.length>0,'No Zhou delivery recorded')
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
 })
 await check('revocation reaches coach evidence while ordinary replies remain available',async e=>{
   s.confirmHealthRevoke()
   s.goToCoachStudentDetail('lin-che')
   e.visibleEvidence=plain(s.selectedStudentEvidence.value)
   assert.ok(e.visibleEvidence.every(x=>x.kind==='plan'),'Revoked training/feedback/diet evidence remained in coach selector')
   s.replyToStudent('lin-che')
   assert.equal(s.state.screen,'messages')
   assert.equal(s.ui.activeConversationId,'conv-gu-yan')
   s.setMessageDraft('conv-gu-yan','周六的安排可以晚半小时吗')
   e.ordinarySent=s.sendMessage('conv-gu-yan')
   assert.equal(e.ordinarySent,true,'Health revocation incorrectly blocked ordinary arrangement reply')
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
