import assert from 'node:assert/strict';
import test from 'node:test';
import { formatIssues, check } from './check_ts_format.mjs';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

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


test('unchanged formatter edits in documentation comments are not reported', () => {
  const source = '/** Shared editing rules.\n * The caller owns storage.\n * No remote success is implied.\n */\nexport const value = 1\n';
  assert.deepEqual(formatIssues('sample.ts', source), []);
});

test('only the generated contract is excluded from formatting checks', () => {
  const root = mkdtempSync(path.join(tmpdir(), 'nerae-format-test-'));
  const originalLog = console.log;
  const originalError = console.error;
  try {
    const directory = path.join(root, 'packages/contracts/src');
    mkdirSync(directory, { recursive: true });
    writeFileSync(path.join(directory, 'generated-types.ts'), 'const generated=1\n');
    writeFileSync(path.join(directory, 'index.ts'), 'export const value = 1\n');
    console.log = () => {};
    console.error = () => {};
    assert.equal(check(root), 0);
    writeFileSync(path.join(directory, 'handwritten.ts'), 'const handwritten=1\n');
    assert.equal(check(root), 1);
  } finally {
    console.log = originalLog;
    console.error = originalError;
    rmSync(root, { recursive: true, force: true });
  }
});
