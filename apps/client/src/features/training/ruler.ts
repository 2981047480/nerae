/** Convert a horizontal ruler gesture to a value without treating an untouched blank as zero. */
export function valueAfterRulerDrag(value: string, deltaPx: number, pixelsPerTick: number, step: number): string | null {
  if (!Number.isFinite(deltaPx) || !Number.isFinite(pixelsPerTick) || pixelsPerTick <= 0 ||
      !Number.isFinite(step) || step <= 0) return null
  const raw = value.trim()
  if (raw && !/^\d+(?:\.\d{1,6})?$/.test(raw)) return null
  const steps = Math.sign(-deltaPx) * Math.round(Math.abs(deltaPx) / pixelsPerTick)
  if (!Number.isSafeInteger(steps) || steps === 0) return null
  const stepText = String(step)
  if (!/^\d+(?:\.\d{1,6})?$/.test(stepText)) return null
  const precision = Math.max((raw.split('.')[1] || '').length, (stepText.split('.')[1] || '').length)
  const scale = 10 ** precision
  const original = Math.round(Number(raw || 0) * scale)
  const increment = Math.round(step * scale)
  const offset = steps * increment
  if (!Number.isSafeInteger(original) || !Number.isSafeInteger(increment) || !Number.isSafeInteger(offset)) return null
  const result = Math.max(0, original + offset)
  if (!Number.isSafeInteger(result)) return null
  const whole = Math.floor(result / scale)
  const fraction = result % scale
  return fraction ? `${whole}.${String(fraction).padStart(precision, '0').replace(/0+$/, '')}` : String(whole)
}
