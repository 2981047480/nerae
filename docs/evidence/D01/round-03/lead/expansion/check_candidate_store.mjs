// Lead-only diagnostic: actual candidate store with fake window/storage.
// No actual-browser or acceptance claim. Source stability is measured.
import { readFile } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
const timers = new Set(), intervals = new Set(), data = new Map()
let accepting = true
const hashFile = async path => createHash('sha256').update(await readFile(path)).digest('hex')
const sourceBefore = await hashFile('src/state/store.ts')
globalThis.window = {
  scrollY: 0,
  localStorage: { getItem:k=>data.get(k)??null, setItem:(k,v)=>data.set(k,v), removeItem:k=>data.delete(k) },
  setTimeout(fn,ms) { if(!accepting)return 0;const id=setTimeout(()=>{timers.delete(id);fn()},ms);timers.add(id);return id },
  clearTimeout(id) { clearTimeout(id);timers.delete(id) },
  setInterval(fn,ms) {const id=setInterval(fn,ms);intervals.add(id);return id},
  clearInterval(id) {clearInterval(id);intervals.delete(id)},
  addEventListener(){},scrollTo(){},
  matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){}}),
}
const {createServer}=await import(pathToFileURL(resolve('node_modules/vite/dist/node/index.js')).href)
const server=await createServer({root:process.cwd(),configFile:false,cacheDir:'/private/tmp/nerae-lead-extended-check',server:{middlewareMode:true,hmr:false,ws:false},appType:'custom'})
const report={scope:'actual candidate store; fake window/storage; no browser claim',sourceBefore,checks:{},status:'partial'}
try{
 const s=await server.ssrLoadModule('/src/state/store.ts')
 s.goToPlanDetail('plan-dumbbell')
 await new Promise(r=>setTimeout(r,300))
 const selectedBefore=s.selectedPlanItem.value?.id
 const reloaded=await server.ssrLoadModule('/src/state/store.ts?lead-cold-reload=1')
 report.checks.detailReload={screenBefore:s.state.screen,selectedBefore,screenReloaded:reloaded.state.screen,selectedReloaded:reloaded.selectedPlanItem.value?.id,sameObject:selectedBefore===reloaded.selectedPlanItem.value?.id}
 s.resetPreview()
 const oldSessionId=s.state.session.id
 const oldDone=s.state.session.exercises.flatMap(e=>e.sets).filter(x=>x.status==='done').map(x=>({id:x.id,weightKg:x.weightKg,reps:x.reps,seconds:x.seconds,completedAt:x.completedAt}))
 const request=s.requestStartPlan('plan-dumbbell')
 if(request==='replace')s.confirmReplaceAndStart()
 const newState=JSON.parse(JSON.stringify(s.state))
 const persistedSets=[...newState.records.flatMap(r=>r.sets),...newState.session.exercises.flatMap(e=>e.sets)]
 report.checks.replacementHistory={oldSessionId,newSessionId:s.state.session.id,request,oldConfirmedSetCount:oldDone.length,retainedIds:oldDone.filter(old=>persistedSets.some(n=>n.id===old.id)).map(x=>x.id),lostIds:oldDone.filter(old=>!persistedSets.some(n=>n.id===old.id)).map(x=>x.id),allRetained:oldDone.every(old=>persistedSets.some(n=>n.id===old.id))}
 report.status='diagnostic_completed'
}catch(error){report.status='failed';report.error=String(error?.stack??error);process.exitCode=1}
finally{
 accepting=false;for(const id of timers)clearTimeout(id);for(const id of intervals)clearInterval(id);await server.close()
 report.sourceAfter=await hashFile('src/state/store.ts');report.sourceStableDuringCheck=report.sourceBefore===report.sourceAfter
 console.log(JSON.stringify(report,null,2))
}
