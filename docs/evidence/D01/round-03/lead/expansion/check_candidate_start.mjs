// Lead-only diagnostic of real modules, using fake window/storage.
// This is not browser, runtime API, or acceptance evidence.
import { readFile } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
const timers = new Set(), intervals = new Set(), data = new Map()
let accepting = true
const hash = async p => createHash('sha256').update(await readFile(p)).digest('hex')
const paths = ['src/state/store.ts', 'src/data/sample.ts', 'src/data/extended.ts']
const before = Object.fromEntries(await Promise.all(paths.map(async p => [p, await hash(p)])))
globalThis.window = {
  scrollY: 0,
  localStorage: { getItem:k=>data.get(k)??null, setItem:(k,v)=>data.set(k,v), removeItem:k=>data.delete(k) },
  setTimeout(fn,ms) { if(!accepting)return 0;const id=setTimeout(()=>{timers.delete(id);fn()},ms);timers.add(id);return id },
  clearTimeout(id) {clearTimeout(id);timers.delete(id)},
  setInterval(fn,ms) {const id=setInterval(fn,ms);intervals.add(id);return id},
  clearInterval(id) {clearInterval(id);intervals.delete(id)},
  addEventListener(){},scrollTo(){},
  matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){}}),
}
const {createServer}=await import(pathToFileURL(resolve('node_modules/vite/dist/node/index.js')).href)
const server=await createServer({root:process.cwd(),configFile:false,cacheDir:'/private/tmp/nerae-lead-start-check',server:{middlewareMode:true,hmr:false,ws:false},appType:'custom'})
const report={scope:'actual candidate modules; fake window/storage; not browser or accepted evidence',before,checks:{},status:'partial'}
try {
  const s=await server.ssrLoadModule('/src/state/store.ts')
  function start(id) {
    s.goToPlanDetail(id)
    let result=s.requestStartPlan(id)
    if(result==='adult') {s.confirmAdultStart();result=s.ui.sheet==='replace'?'replace':'started'}
    if(result==='replace')s.confirmReplaceAndStart()
    return result
  }
  s.resetPreview()
  s.goToPlanDetail('plan-weekly-base')
  s.selectPlanDay('plan-weekly-base-day-b')
  const selectedDay=s.activePlanDay.value?.title
  const selectedId=s.activePlanDay.value?.id
  const request=s.requestStartPlan('plan-weekly-base')
  if(request==='replace')s.confirmReplaceAndStart()
  report.checks.selectedDayStart={selectedId,selectedDay,actualDay:s.state.session.dayTitle,matches:selectedDay===s.state.session.dayTitle,request}
  s.resetPreview()
  start('plan-dumbbell')
  const firstSession=s.state.session.id
  const firstEntry=s.currentEntry.value
  s.setInputValue(firstEntry.set.id,'weightKg','12')
  s.setInputValue(firstEntry.set.id,'reps','8')
  const firstConfirmed=s.completeCurrentSet()
  const firstFact=JSON.parse(JSON.stringify(firstEntry.set))
  start('plan-core')
  start('plan-dumbbell')
  const secondSession=s.state.session.id
  const secondEntry=s.currentEntry.value
  const secondInitialStatus=secondEntry.set.status
  s.setInputValue(secondEntry.set.id,'weightKg','17')
  s.setInputValue(secondEntry.set.id,'reps','9')
  const secondConfirmed=s.completeCurrentSet()
  const secondFact=JSON.parse(JSON.stringify(secondEntry.set))
  start('plan-core')
  const archived=s.state.records.filter(r=>r.id===`archived-${firstSession}`)
  const stored=archived.flatMap(r=>r.sets).filter(x=>x.id===firstFact.id)
  report.checks.repeatStart={firstSession,secondSession,sameSessionId:firstSession===secondSession,secondInitialStatus,firstConfirmed,secondConfirmed,firstFact,secondFact,archivedRows:archived.length,storedFacts:stored,firstRetained:stored.some(x=>x.weightKg===12&&x.reps===8),secondRetained:stored.some(x=>x.weightKg===17&&x.reps===9)}
  report.status='diagnostic_completed'
} catch(error) {report.status='failed';report.error=String(error?.stack??error);process.exitCode=1}
finally {
  accepting=false;for(const id of timers)clearTimeout(id);for(const id of intervals)clearInterval(id);await server.close()
  report.after=Object.fromEntries(await Promise.all(paths.map(async p=>[p,await hash(p)])))
  report.sourceStable=JSON.stringify(before)===JSON.stringify(report.after)
  console.log(JSON.stringify(report,null,2))
}
