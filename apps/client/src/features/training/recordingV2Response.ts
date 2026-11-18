import { validV2Key, validV2Set } from './operationJournal'
import type { RecordingV2Detail } from './recordingV2Projection'

function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}
function shape(value: unknown, required: string[], optional: string[] = []): value is Record<string, unknown> {
  if (!object(value)) return false
  return required.every(key => Object.prototype.hasOwnProperty.call(value, key)) &&
    Object.keys(value).every(key => required.includes(key) || optional.includes(key))
}
function validDate(value: unknown): value is string {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return false
  const date = new Date(`${value}T00:00:00.000Z`)
  return !Number.isNaN(date.getTime()) && date.toISOString().slice(0, 10) === value
}
function validTime(value: unknown): value is string {
  if (typeof value !== 'string' || value.length > 64) return false
  const match = /^(\d{4}-\d{2}-\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/.exec(value)
  return Boolean(match && validDate(match[1]) && Number(match[2]) < 24 && Number(match[3]) < 60 && Number(match[4]) < 60 && !Number.isNaN(Date.parse(value)))
}
function positiveInteger(value: unknown): value is number { return Number.isSafeInteger(value) && Number(value) >= 1 }
function validVolume(value: unknown): value is Record<string, unknown> {
  const count = (number: unknown) => Number.isSafeInteger(number) && Number(number) >= 0
  return shape(value, ['fullSets','partialSets','excludedSets'], ['calculatedKg']) &&
    count(value.fullSets) && count(value.partialSets) && count(value.excludedSets) &&
    (value.calculatedKg === undefined || (typeof value.calculatedKg === 'string' && value.calculatedKg.length <= 32 &&
      /^\d+(?:\.\d{1,2})?$/.test(value.calculatedKg))) &&
    ((Number(value.fullSets) + Number(value.partialSets) > 0) === (value.calculatedKg !== undefined))
}
function hasUnicode(value: string): boolean { return !/[\uD800-\uDFFF]/.test(value.replace(/[\uD800-\uDBFF][\uDC00-\uDFFF]/g, '')) }
function freeze<T>(value: T): T {
  if (value && typeof value === 'object') {
    for (const item of Object.values(value as Record<string, unknown>)) freeze(item)
    Object.freeze(value)
  }
  return value
}

/** Validate the complete server snapshot before exposing it to local merge. */
export function parseRecordingV2Detail(raw: unknown, expectedId: string): RecordingV2Detail {
  let copy: unknown
  try {
    const json = JSON.stringify(raw)
    if (typeof json !== 'string' || json.length > 4 * 1024 * 1024) throw new Error('invalid')
    copy = JSON.parse(json)
  } catch { throw new Error('invalid recording response') }
  const required = ['id','version','source','date','timeZone','clientEventId','status','mode','createdAt','updatedAt','entries','sets','removedSets']
  if (!shape(copy, required, ['recordedAt','actionVolumes']) || copy.id !== expectedId || !/^[1-9]\d{0,19}$/.test(expectedId) ||
      !positiveInteger(copy.version) || copy.source !== 'free' || !validDate(copy.date) || typeof copy.timeZone !== 'string' ||
      !copy.timeZone || Array.from(copy.timeZone).length > 64 || !hasUnicode(copy.timeZone) || !validV2Key(copy.clientEventId) ||
      (copy.status !== 'in_progress' && copy.status !== 'completed') || (copy.mode !== 'after_workout' && copy.mode !== 'per_set') ||
      !validTime(copy.createdAt) || !validTime(copy.updatedAt) ||
      (copy.status === 'completed' ? !validTime(copy.recordedAt) : copy.recordedAt !== undefined) ||
      !Array.isArray(copy.entries) || copy.entries.length > 2000 || !Array.isArray(copy.sets) || !Array.isArray(copy.removedSets) ||
      copy.sets.length + copy.removedSets.length > 10000) throw new Error('invalid recording response')
  const entries = new Map<string, boolean>()
  const names = new Map<string, string>()
  const volumes = new Map<string, Record<string, unknown>>()
  const activeCounts = new Map<string, number>()
  for (const entry of copy.entries) {
    if (!shape(entry, ['exerciseEntryKey','source','exerciseName'], ['removedPosition','volume']) || !validV2Key(entry.exerciseEntryKey) ||
        entry.source !== 'custom' || typeof entry.exerciseName !== 'string' || !entry.exerciseName.trim() ||
        Array.from(entry.exerciseName).length > 60 || !hasUnicode(entry.exerciseName) ||
        (entry.removedPosition !== undefined && !positiveInteger(entry.removedPosition)) || entries.has(entry.exerciseEntryKey)) throw new Error('invalid recording response')
    entries.set(entry.exerciseEntryKey, entry.removedPosition !== undefined)
    names.set(entry.exerciseEntryKey, entry.exerciseName)
    if (entry.volume !== undefined) {
      if (entry.removedPosition !== undefined || !validVolume(entry.volume)) throw new Error('invalid recording response')
      volumes.set(entry.exerciseEntryKey, entry.volume)
    }
  }
  const records = new Set<string>(); const indexes = new Set<string>(); let active = 0
  const check = (item: unknown, removed: boolean): void => {
    if (!validV2Set(item) || !object(item)) throw new Error('invalid recording response')
    const record = item.recordKey as string; const entry = item.exerciseEntryKey as string; const index = `${entry}\0${item.setIndex}`
    if (!entries.has(entry) || records.has(record) || indexes.has(index)) throw new Error('invalid recording response')
    records.add(record); indexes.add(index)
    if (!removed && !entries.get(entry)) { active++; activeCounts.set(entry, (activeCounts.get(entry) || 0) + 1) }
  }
  for (const set of copy.sets) check(set, false)
  for (const item of copy.removedSets) {
    if (!shape(item,['set','removedPosition']) || !positiveInteger(item.removedPosition)) throw new Error('invalid recording response')
    check(item.set, true)
  }
  for (const [entry, volume] of volumes) {
    if (Number(volume.fullSets) + Number(volume.partialSets) + Number(volume.excludedSets) !== (activeCounts.get(entry) || 0)) throw new Error('invalid recording response')
  }
  if (copy.actionVolumes !== undefined) {
    if (!Array.isArray(copy.actionVolumes) || copy.actionVolumes.length > 2000) throw new Error('invalid recording response')
    const covered = new Set<string>(); const groupedNames = new Set<string>()
    for (const action of copy.actionVolumes) {
      if (!shape(action, ['exerciseEntryKeys','volume']) || !Array.isArray(action.exerciseEntryKeys) ||
          action.exerciseEntryKeys.length < 1 || action.exerciseEntryKeys.length > 2000 || !validVolume(action.volume)) throw new Error('invalid recording response')
      let name: string | undefined; let count = 0
      for (const key of action.exerciseEntryKeys) {
        if (!validV2Key(key) || covered.has(key) || entries.get(key) !== false || !activeCounts.get(key)) throw new Error('invalid recording response')
        const currentName = names.get(key)
        if (name !== undefined && currentName !== name) throw new Error('invalid recording response')
        name = currentName; covered.add(key); count += activeCounts.get(key) || 0
      }
      if (name === undefined || groupedNames.has(name) ||
          Number(action.volume.fullSets) + Number(action.volume.partialSets) + Number(action.volume.excludedSets) !== count) throw new Error('invalid recording response')
      groupedNames.add(name)
    }
    if ([...activeCounts.keys()].some(key => !covered.has(key))) throw new Error('invalid recording response')
  }
  if (copy.status === 'completed' && active === 0) throw new Error('invalid recording response')
  return freeze(copy as unknown as RecordingV2Detail)
}

export interface RecordingV2Page {
  page: number
  pageSize: number
  total: number
  items: { id: string; version: number; recordFormat: 1 | 2; date: string; timeZone: string; status: 'in_progress' | 'completed' | 'skipped' }[]
}

export function parseRecordingV2Page(raw: unknown, page: number, pageSize: number): RecordingV2Page {
  let copy: unknown
  try {
    const json = JSON.stringify(raw)
    if (typeof json !== 'string' || json.length > 1024 * 1024) throw new Error('invalid')
    copy = JSON.parse(json)
  } catch { throw new Error('invalid recording page') }
  if (!shape(copy, ['items','page','pageSize','total']) || copy.page !== page || copy.pageSize !== pageSize ||
      !Number.isSafeInteger(copy.total) || Number(copy.total) < 0 || !Array.isArray(copy.items)) throw new Error('invalid recording page')
  const offset = (page - 1) * pageSize
  const maximum = Number.isSafeInteger(offset) ? Math.max(0, Math.min(pageSize, Number(copy.total) - offset)) : 0
  if (copy.items.length > maximum) throw new Error('invalid recording page')
  const ids = new Set<string>()
  for (const item of copy.items) {
    if (!shape(item,['id','version','recordFormat','date','timeZone','status']) || typeof item.id !== 'string' ||
        !/^[1-9]\d{0,19}$/.test(item.id) || ids.has(item.id) || !positiveInteger(item.version) ||
        (item.recordFormat !== 1 && item.recordFormat !== 2) || !validDate(item.date) || typeof item.timeZone !== 'string' ||
        !item.timeZone || Array.from(item.timeZone).length > 64 || !hasUnicode(item.timeZone) ||
        (item.status !== 'in_progress' && item.status !== 'completed' && (item.status !== 'skipped' || item.recordFormat !== 1))) throw new Error('invalid recording page')
    ids.add(item.id)
  }
  return freeze(copy as unknown as RecordingV2Page)
}
