import assert from 'node:assert/strict';
import test from 'node:test';
import { formatIssues } from './check_ts_format.mjs';

test('formatted TypeScript is accepted', () => {
  assert.deepEqual(formatIssues('sample.ts', "export function value() {\n  return { value: 1 }\n}\n"), []);
});

test('misformatted TypeScript is rejected', () => {
  assert.ok(formatIssues('sample.ts', 'const value=1\n').some(issue => issue.includes('TypeScript formatting')));
});

test('Vue script formatting is checked', () => {
  assert.ok(formatIssues('sample.vue', '<script setup lang="ts">\nconst value=1\n</script>\n').some(issue => issue.includes('TypeScript formatting')));
});

test('missing final newline and trailing whitespace are rejected', () => {
  assert.ok(formatIssues('sample.ts', 'const value = 1 ').includes('missing final newline'));
  assert.ok(formatIssues('sample.ts', 'const value = 1 \n').includes('trailing whitespace'));
});
