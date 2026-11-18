<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { ArrowLeft, Plus, Check, ChevronDown, Trash2, ArrowRight, AlertCircle } from '@lucide/vue'
import BaseSheet from '../components/BaseSheet.vue'
import {
  state, leaveTraining, storageState, retryPersist, setInputValue,
  logInput, logErrors, logMessage, recordLogSet, saveTrainingLog,
  setRecordingMode, setTrainingDate, localToday, usePreviousLogSet,
  addLogSet, addLogExercise, removeLogSet, removeLogExercise,
  activeConflict, resolveSetConflict, doneSets,
} from '../state/store'
import { isBodyweightKind, isDurationKind, isPerSideKind, lastReferenceLabel } from '../utils/format'
import type { LoadKind, SessionExercise, SessionSet, SetInput } from '../types'

const selected = ref<string | undefined>(state.session.selectedExerciseId ?? state.session.exercises.find(e => e.sets.some(s => s.status === 'pending'))?.id ?? state.session.exercises[0]?.id)
const mode = computed(() => state.recordingMode)
const addOpen = ref(false)
const name = ref('')
const kind = ref<LoadKind>('weight')
const addError = ref('')
const dateError = ref('')
const pendingDelete = ref<{exerciseId:string;setId?:string}|null>(null)
const removed = ref<{exerciseId:string; exercise?:SessionExercise; set?:SessionSet; position:number; inputs:Record<string,SetInput>}|null>(null)
watch(selected,id=>{ state.session.selectedExerciseId=id },{immediate:true})
const visible = computed(() => mode.value === 'after' ? state.session.exercises : state.session.exercises.filter(e => e.id === selected.value))
const filledCount = computed(() => state.session.exercises.reduce((sum,e) => sum+e.sets.filter(s => Object.values(logInput(e,s)).some(v=>v.trim())).length,0))
const choices = ['哑铃卧推','高位下拉','深蹲','罗马尼亚硬拉','坐姿划船','哑铃肩推','俯卧撑','平板支撑']

function input(_e:SessionExercise,s:SessionSet,field:keyof SetInput,event:Event) {
  setInputValue(s.id,field,(event.target as HTMLInputElement).value)
  delete logErrors[s.id]
  logMessage.text = ''
}
function changeDate(event:Event) {
  const field=event.target as HTMLInputElement
  if(setTrainingDate(field.value)) dateError.value=''
  else {
    field.value=state.session.trainingDate ?? localToday()
    dateError.value='请选择今天或更早的有效训练日期。'
  }
}
function previous(exercise:SessionExercise) {
  for(const set of exercise.sets) {
    if(set.status === 'pending' && !Object.values(logInput(exercise,set)).some(v=>v.trim())) usePreviousLogSet(set.id)
  }
}
function createExercise() {
  const id = addLogExercise(name.value,kind.value)
  if(!id) {addError.value='请输入 1–60 字的动作名称（最多 30 个动作）。';return}
  selected.value=id; addOpen.value=false;name.value='';addError.value=''
}
function removeConfirmed() {
  if(!pendingDelete.value) return
  const target=pendingDelete.value
  const exercise=state.session.exercises.find(e=>e.id===target.exerciseId)
  if(!exercise) return
  const set=exercise.sets.find(s=>s.id===target.setId)
  const ids=target.setId?[target.setId]:exercise.sets.map(s=>s.id)
  const snapshot={exerciseId:exercise.id,exercise:target.setId?undefined:exercise,set,position:target.setId?exercise.sets.findIndex(s=>s.id===target.setId):state.session.exercises.indexOf(exercise),inputs:Object.fromEntries(ids.filter(id=>state.inputs[id]).map(id=>[id,state.inputs[id]]))}
  if(target.setId) removeLogSet(target.setId)
  else removeLogExercise(target.exerciseId)
  const remaining=state.session.exercises.find(e=>e.id===target.exerciseId)
  const deleted=target.setId?!remaining?.sets.some(s=>s.id===target.setId):!remaining
  if(deleted) removed.value=JSON.parse(JSON.stringify(snapshot))
  if(!state.session.exercises.some(e=>e.id===selected.value)) selected.value=state.session.exercises[0]?.id
  pendingDelete.value=null
}
function undo() {
  if(!removed.value) return
  const item=removed.value
  if(item.exercise && !state.session.exercises.some(e=>e.id===item.exerciseId)) state.session.exercises.splice(item.position,0,item.exercise)
  else if(item.set) {
    const exercise=state.session.exercises.find(e=>e.id===item.exerciseId)
    if(exercise && !exercise.sets.some(s=>s.id===item.set!.id)) { exercise.sets.splice(item.position,0,item.set);exercise.sets.forEach((s,i)=>{s.index=i+1}) }
  }
  Object.assign(state.inputs,item.inputs)
  selected.value=item.exerciseId
  removed.value=null
}
async function save() {
  const ok=saveTrainingLog()
  if(!ok) {
    const bad=Object.keys(logErrors).find(id=>Object.values(logErrors[id]??{}).some(Boolean))
    if(bad) selected.value=state.session.exercises.find(e=>e.sets.some(s=>s.id===bad))?.id
    await nextTick()
    document.querySelector<HTMLInputElement>('[aria-invalid="true"]')?.focus()
  }
}
</script>

<template>
  <main id="main" class="log-page" tabindex="-1">
    <header class="log-nav">
      <button type="button" class="icon-btn" aria-label="返回，保留记录草稿" @click="leaveTraining"><ArrowLeft :size="21" /></button>
      <span>训练记录</span>
      <span class="log-local">{{ state.session.status==='complete' && storageState.ok?'已保存':'本机草稿' }}</span>
    </header>
    <div class="log-content">
      <div class="log-heading"><p class="eyebrow">TRAINING LOG</p><h1>{{ state.session.dayTitle }}</h1>
        <label class="log-date">训练日期 <input type="date" aria-label="训练日期" :max="localToday()" :value="state.session.trainingDate ?? localToday()" :aria-invalid="Boolean(dateError)" @change="changeDate" /></label>
        <p v-if="dateError" class="field-error" role="alert">{{ dateError }}</p>
      </div>
      <div class="log-mode" role="group" aria-label="记录方式">
        <button type="button" :aria-pressed="mode==='after'" @click="setRecordingMode('after')">训练后记录</button>
        <button type="button" :aria-pressed="mode==='set'" @click="setRecordingMode('set')">每组完成后记录</button>
      </div>
      <p class="log-mode-help">{{ mode==='after' ? '按实际完成填写，空白组不会计入记录。' : '练完一组，记下一组。也可以随时换个动作。' }}</p>
      <div v-if="!storageState.ok || state.preview.training==='sync-failed'" class="log-error" role="alert"><AlertCircle :size="18"/><span>未能保存到本机，当前输入仍保留。</span><button type="button" @click="retryPersist">重试</button></div>
      <section v-if="activeConflict" class="log-conflict" role="alert"><h2>这组有另一份记录</h2><p>{{ activeConflict.exerciseName }} · 第 {{ activeConflict.setIndex }} 组</p><p>本机 {{ activeConflict.localText }} · 另一份 {{ activeConflict.remoteText }}</p><div class="btn-row"><button type="button" class="btn" @click="resolveSetConflict('local')">保留本机记录</button><button type="button" class="btn btn--ghost" @click="resolveSetConflict('remote')">采用另一份</button></div></section>
      <div v-if="removed" class="log-undo" role="status"><span>已移除，保存后更新记录。</span><button type="button" @click="undo">撤销</button></div>
      <nav v-if="mode==='set' && state.session.exercises.length" class="exercise-picker" aria-label="选择要记录的动作">
        <button v-for="(e,i) in state.session.exercises" :key="e.id" type="button" :aria-pressed="selected===e.id" @click="selected=e.id"><span class="num">{{ String(i+1).padStart(2,'0') }}</span>{{ e.name }}<Check v-if="e.sets.length && e.sets.every(s=>s.status==='done')" :size="14" /></button>
      </nav>
      <section v-for="exercise in visible" :key="exercise.id" class="log-exercise" :data-exercise="exercise.id">
        <header class="log-exercise-head"><span class="exercise-index num">{{ String(state.session.exercises.indexOf(exercise)+1).padStart(2,'0') }}</span>
          <div class="log-exercise-title"><h2>{{ exercise.name }}</h2><p v-if="exercise.lastRef.dateLabel!=='暂无可比较参考'">上次 {{ lastReferenceLabel(exercise.lastRef,exercise.kind) }}</p><p v-else>暂无历史参考</p></div>
          <button type="button" class="icon-btn log-remove" :aria-label="`移除动作 ${exercise.name}`" @click="pendingDelete={exerciseId:exercise.id}"><Trash2 :size="17" /></button>
        </header>
        <div class="log-table" :class="{'log-table--duration':isDurationKind(exercise.kind),'log-table--bodyweight':isBodyweightKind(exercise.kind),'log-table--each':mode==='set'}">
          <div class="log-table-labels"><span>组</span><span v-if="!isDurationKind(exercise.kind) && !isBodyweightKind(exercise.kind)">重量 <small>kg</small></span><span>{{ isDurationKind(exercise.kind)?'实际秒数':exercise.kind==='breath'?'呼吸次数':isPerSideKind(exercise.kind)?'次数 / 侧':'次数' }}</span><span class="sr-only">操作</span></div>
          <div v-for="set in exercise.sets" :key="set.id" class="log-set" :class="{'log-set--recorded':set.status==='done'}" :data-set="set.id">
            <div class="log-set-fields"><span class="set-index num">{{ set.index }}</span>
              <input v-if="!isDurationKind(exercise.kind) && !isBodyweightKind(exercise.kind)" type="text" inputmode="decimal" autocomplete="off" placeholder="—" :aria-label="`${exercise.name} 第 ${set.index} 组重量 kg`" :aria-invalid="Boolean(logErrors[set.id]?.weightKg)" :value="logInput(exercise,set).weightKg" @input="input(exercise,set,'weightKg',$event)" />
              <input v-if="!isDurationKind(exercise.kind)" type="text" inputmode="numeric" autocomplete="off" placeholder="—" :aria-label="`${exercise.name} 第 ${set.index} 组${exercise.kind==='breath'?'呼吸次数':'次数'}`" :aria-invalid="Boolean(logErrors[set.id]?.reps)" :value="logInput(exercise,set).reps" @input="input(exercise,set,'reps',$event)" />
              <input v-else type="text" inputmode="numeric" autocomplete="off" placeholder="—" :aria-label="`${exercise.name} 第 ${set.index} 组实际秒数`" :aria-invalid="Boolean(logErrors[set.id]?.seconds)" :value="logInput(exercise,set).seconds" @input="input(exercise,set,'seconds',$event)" />
              <button type="button" class="icon-btn log-remove" :aria-label="`移除 ${exercise.name} 第 ${set.index} 组`" @click="pendingDelete={exerciseId:exercise.id,setId:set.id}"><Trash2 :size="15" /></button>
            </div>
            <p v-if="Object.values(logErrors[set.id]??{}).some(Boolean)" class="field-error" role="alert">{{ Object.values(logErrors[set.id]??{}).filter(Boolean).join('；') }}</p>
            <button v-if="mode==='set'" type="button" :aria-label="`${exercise.name} 第 ${set.index} 组${set.status==='done'?'更新记录':'记下这组'}`" class="log-record-set" :class="{'log-record-set--done':set.status==='done'}" @click="recordLogSet(set.id)"><Check v-if="set.status==='done'" :size="15"/>{{ set.status==='done'?'更新':'记下' }}</button>
          </div>
        </div>
        <div class="log-exercise-actions"><button type="button" :disabled="exercise.sets.length>=30" @click="addLogSet(exercise.id)"><Plus :size="16"/>添加一组</button><button v-if="exercise.lastRef.dateLabel!=='暂无可比较参考'" type="button" @click="previous(exercise)">空白组沿用上次</button></div>
        <details v-if="exercise.cue" class="log-cue"><summary>动作提示<ChevronDown :size="14"/></summary><p>{{ exercise.cue }}</p></details>
      </section>
      <div v-if="!state.session.exercises.length" class="log-empty"><h2>这次练了什么？</h2><p>先添加一个动作，再填实际完成的组。</p></div>
      <button type="button" class="log-add-exercise" @click="addOpen=true"><Plus :size="20"/>添加动作</button>
      <p v-if="logMessage.text" class="log-result" :class="{'log-result--error':logMessage.error}" :role="logMessage.error?'alert':'status'">{{ logMessage.text }}</p>
      <p class="log-footnote">只记实际完成的数据。退出后可继续填写。</p>
    </div>
    <footer class="log-savebar"><div><strong class="num">{{ filledCount }} 组</strong><span>{{ state.session.status==='complete' && storageState.ok?'已保存到本机':mode==='set'?`${doneSets} 组已记下`:'待保存' }}</span></div><button type="button" class="btn btn--accent" :disabled="Boolean(activeConflict)" @click="save">{{ state.session.status==='complete'?'更新记录':'保存记录' }}<ArrowRight :size="17"/></button></footer>
    <BaseSheet :open="addOpen" title="添加动作" @close="addOpen=false"><form id="add-exercise-form" class="log-add-form" @submit.prevent="createExercise"><label>动作名称<input v-model="name" maxlength="60" placeholder="搜索或填写动作名称" autofocus /></label><div class="log-suggestions"><button v-for="choice in choices" :key="choice" type="button" @click="name=choice;kind=choice==='平板支撑'?'duration':choice==='俯卧撑'?'bodyweight':'weight'">{{ choice }}</button></div><label>记录内容<select v-model="kind"><option value="weight">重量与次数</option><option value="perSide">重量与每侧次数</option><option value="bodyweight">自重次数</option><option value="bodyweightPerSide">自重每侧次数</option><option value="duration">实际秒数</option><option value="durationPerSide">每侧实际秒数</option><option value="breath">呼吸次数</option></select></label><p v-if="addError" class="field-error" role="alert">{{ addError }}</p></form><template #footer><button type="submit" form="add-exercise-form" class="btn btn--accent">添加动作</button></template></BaseSheet>
    <BaseSheet :open="Boolean(pendingDelete)" :title="pendingDelete?.setId?'移除这一组？':'移除这个动作？'" @close="pendingDelete=null"><p>该项不会计入本次记录。移除后可撤销。</p><template #footer><button type="button" class="btn btn--ghost" @click="pendingDelete=null">保留</button><button type="button" class="btn" @click="removeConfirmed">确认移除</button></template></BaseSheet>
  </main>
</template>
