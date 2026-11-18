import test from 'node:test'
import assert from 'node:assert/strict'
import { valueAfterRulerDrag } from './ruler.ts'

test('untouched or invalid motion does not create a value', () => {
  assert.equal(valueAfterRulerDrag('', 0, 16, 1), null)
  assert.equal(valueAfterRulerDrag('', -7, 16, 1), null)
  assert.equal(valueAfterRulerDrag('12.', -16, 16, 1), null)
  assert.equal(valueAfterRulerDrag('x', -16, 16, 1), null)
  assert.equal(valueAfterRulerDrag('12', Infinity, 16, 1), null)
  assert.equal(valueAfterRulerDrag('12', 16, 0, 1), null)
})

test('dragging preserves decimal starting values and respects the chosen step', () => {
  assert.equal(valueAfterRulerDrag('12.5', -32, 16, 1), '14.5')
  assert.equal(valueAfterRulerDrag('12.5', 32, 16, 0.5), '11.5')
  assert.equal(valueAfterRulerDrag('0.1', -16, 16, 0.2), '0.3')
  assert.equal(valueAfterRulerDrag('12', 8, 16, 1), '11')
  assert.equal(valueAfterRulerDrag('12', -8, 16, 1), '13')
})

test('values cannot turn negative or exceed safe integer math', () => {
  assert.equal(valueAfterRulerDrag('1', 80, 16, 1), '0')
  assert.equal(valueAfterRulerDrag('', -16, 16, 1), '1')
  assert.equal(valueAfterRulerDrag('999999999999999999999999', -16, 16, 1), null)
  assert.equal(valueAfterRulerDrag('1', -1e30, 16, 1), null)
})
