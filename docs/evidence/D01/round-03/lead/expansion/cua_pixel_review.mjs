// Lead QA: normal UI only, with encoded-raster dimensions verified for every accepted capture.
import * as base from './cua_final_review.mjs'
import { capture } from './cua_pixel_capture.mjs'
import { gotoScenario } from '../fix-02/cua_review_v3.mjs'
export const resetSample=base.resetSample
export const waitForSettledView=base.waitForSettledView
export const inspectMotionAndFocus=base.inspectMotionAndFocus
export const inspectFlatTextContrast=base.inspectFlatTextContrast

export async function recordFlow(ctx,name){
  await base.waitForSettledView(ctx)
  let previous=null,stable=0
  for(let attempt=0;attempt<30;attempt++){
    const observed=JSON.stringify(await ctx.tab.playwright.evaluate(()=>({width:innerWidth,height:innerHeight,dpr:devicePixelRatio,x:scrollX,y:scrollY})))
    stable=observed===previous?stable+1:0
    if(stable>=2)break
    if(attempt===29)throw Error('Viewport did not settle before verified capture')
    previous=observed
    await new Promise(r=>setTimeout(r,50))
  }
  const snapshot=await ctx.tab.playwright.domSnapshot()
  await ctx.fs.writeFile(ctx.evidenceDir+'/'+name+'.txt',snapshot,{flag:'wx'})
  const image=await capture(ctx,name)
  return {snapshot,path:image.path,pixelCalibration:image}
}

export async function sweepIsolatedStates(ctx,family,pattern,{role,navigation,afterScenario}={}){
  async function enter(){
    await base.resetSample(ctx)
    if(role){
      await ctx.tab.playwright.getByRole('button',{name:'打开预览控制',exact:true}).click()
      const dialog=ctx.tab.playwright.getByRole('dialog',{name:'预览',exact:true})
      await dialog.getByRole('button',{name:role,exact:true}).click()
      await dialog.waitFor({state:'hidden'})
      await base.waitForSettledView(ctx)
    }
    if(navigation)await ctx.tab.playwright.getByRole('navigation',{name:navigation.landmark,exact:true}).getByRole('button',{name:navigation.button,exact:true}).click()
    else await gotoScenario(ctx,pattern)
    await base.waitForSettledView(ctx)
    if(afterScenario)await afterScenario(ctx)
    await base.waitForSettledView(ctx)
  }
  await enter()
  await ctx.tab.playwright.getByRole('button',{name:'打开预览控制',exact:true}).click()
  const labels=await ctx.tab.playwright.evaluate(()=>[...document.querySelectorAll('.state-chip')].map(e=>e.textContent.trim()))
  await base.closeObservedSheet(ctx)
  await base.waitForSettledView(ctx)
  const rows=[]
  for(let index=0;index<labels.length;index++){
    const label=labels[index]
    await enter()
    await ctx.tab.playwright.getByRole('button',{name:'打开预览控制',exact:true}).click()
    const dialog=ctx.tab.playwright.getByRole('dialog',{name:'预览',exact:true})
    await dialog.getByRole('button',{name:label,exact:true}).click()
    if(await dialog.isVisible())await base.closeObservedSheet(ctx)
    const name='state-'+family+'-'+String(index).padStart(2,'0')
    const evidence=await recordFlow(ctx,name)
    const row={family,label,snapshot:name+'.txt',screenshot:evidence.path,pixelVerified:evidence.pixelCalibration.valid,headingsAndFeedback:evidence.snapshot.split('\n').filter(line=>/heading|paragraph:|status|alert/.test(line))}
    rows.push(row)
    await ctx.fs.appendFile(ctx.evidenceDir+'/state-cases.jsonl',JSON.stringify({sourceRevision:ctx.sourceRevision,resetBeforeEachState:true,...row})+'\n')
  }
  const result={family,sourceRevision:ctx.sourceRevision,resetBeforeEachState:true,accepted:false,rows}
  await ctx.fs.appendFile(ctx.evidenceDir+'/states-isolated-summary.jsonl',JSON.stringify(result)+'\n')
  return result
}
