// Lead-only artifact calibration. Reads actual encoded pixels; never changes app state.
export function encodedDimensions(bytes) {
  const d=bytes instanceof Uint8Array?bytes:new Uint8Array(bytes)
  const u32=i=>d[i]*16777216+d[i+1]*65536+d[i+2]*256+d[i+3]
  if(d[0]===137&&d[1]===80&&d[2]===78&&d[3]===71) return {width:u32(16),height:u32(20),format:'png'}
  if(d[0]===255&&d[1]===216){
    let i=2
    while(i+8<d.length){
      if(d[i]!==255){i++;continue}
      const tag=d[i+1];i+=2
      if(tag===216||tag===217)continue
      const n=d[i]*256+d[i+1]
      if([192,193,194,195,197,198,199,201,202,203,205,206,207].includes(tag))return {width:d[i+5]*256+d[i+6],height:d[i+3]*256+d[i+4],format:'jpg'}
      if(n<2)break
      i+=n
    }
  }
  throw Error('Unsupported screenshot encoding; do not accept without pixel dimensions')
}

export async function capture(ctx,name){
  const inspect=()=>({width:innerWidth,height:innerHeight,dpr:devicePixelRatio,scrollX,scrollY,scale:document.documentElement.dataset.textScale,title:document.querySelector('main h1')?.textContent})
  await ctx.fs.mkdir(ctx.evidenceDir+'/screenshots',{recursive:true})
  for(let attempt=0;attempt<3;attempt++){
    // A fresh rendered state precedes each screenshot, including a failed raster calibration retry.
    await ctx.tab.playwright.domSnapshot()
    const before=await ctx.tab.playwright.evaluate(inspect)
    if(before.dpr!==1)throw Error('This QA capture expects documented emulated DPR1; calibrate another DPR explicitly')
    const bytes=await ctx.tab.screenshot({fullPage:false})
    const pixels=encodedDimensions(bytes)
    const after=await ctx.tab.playwright.evaluate(inspect)
    const stable=['width','height','dpr','scrollX','scrollY','scale','title'].every(k=>before[k]===after[k])
    const valid=stable&&pixels.width===before.width&&pixels.height===before.height
    const path=ctx.evidenceDir+'/screenshots/'+name+(attempt?'-retry'+attempt:'')+(valid?'':'-excluded')+'.'+pixels.format
    await ctx.fs.writeFile(path,bytes,{flag:'wx'})
    const row={name,sourceRevision:ctx.sourceRevision,attempt,before,after,pixels,valid,path,scope:'Encoded screenshot dimensions checked against stable DOM viewport; DPR1 only'}
    await ctx.fs.appendFile(ctx.evidenceDir+'/pixel-captures.jsonl',JSON.stringify(row)+'\n')
    if(valid)return row
  }
  throw Error('Three raster calibration failures; stop this capture path and inspect the browser')
}
