import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { scanClient, scanGo } from './boundary.mjs';

function fixture(t) {
  const root = mkdtempSync(path.join(os.tmpdir(), 'nerae-boundary-test-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  function write(relative, content) {
    const target = path.join(root, relative);
    mkdirSync(path.dirname(target), { recursive: true });
    writeFileSync(target, content);
  }
  write('apps/api/go.mod', 'module nerae/api\n\ngo 1.23\n');
  write('apps/api/internal/modules/alpha/domain/entity.go', 'package domain\nimport _ "fmt"\n');
  write('apps/client/tsconfig.json', '{"compilerOptions":{"moduleResolution":"Bundler","module":"ESNext"}}');
  write('apps/admin/tsconfig.json', '{"compilerOptions":{"moduleResolution":"Bundler","module":"ESNext"}}');
  write('apps/client/src/main.ts', 'export {}\n');
  write('apps/client/src/pages/index.vue', '<script setup lang="ts">\nconst value: number = 1\n</script>');
  return { root, write };
}

test('allowed domain and view imports pass', t => {
  const { root } = fixture(t);
  assert.deepEqual(scanGo(root), []);
  assert.deepEqual(scanClient(root), []);
});

test('domain cannot import database/sql', t => {
  const { root, write } = fixture(t);
  write('apps/api/internal/modules/alpha/domain/entity.go', 'package domain\nimport _ "database/sql"\n');
  assert.ok(scanGo(root).some(item => item.includes('domain imports outer layer database/sql')));
});

test('one module cannot import another module repository', t => {
  const { root, write } = fixture(t);
  write('apps/api/internal/modules/beta/repository/store.go', 'package repository\n');
  write('apps/api/internal/modules/alpha/application/app.go', 'package application\nimport _ "nerae/api/internal/modules/beta/repository"\n');
  assert.ok(scanGo(root).some(item => item.includes("another module's storage")));
});

test('view cannot reach a database package through a local alias', t => {
  const { root, write } = fixture(t);
  write('apps/client/src/pages/index.vue', '<script setup lang="ts">\nimport "@/services/store"\n</script>');
  write('apps/client/src/services/store.ts', 'import "mysql2"\n');
  assert.ok(scanClient(root).some(item => item.includes('mysql2')));
});

test('computed module imports fail closed', t => {
  const { root, write } = fixture(t);
  write('apps/client/src/pages/index.vue', '<script setup lang="ts">\nconst name = "x"; import(name)\n</script>');
  assert.throws(() => scanClient(root), /dynamic module import/);
});

test('unresolved relative imports do not silently pass', t => {
  const { root, write } = fixture(t);
  write('apps/client/src/pages/index.vue', '<script setup lang="ts">\nimport "../missing"\n</script>');
  assert.ok(scanClient(root).some(item => item.includes('unresolved local import')));
});

test('Go files behind custom build tags are still checked', t => {
  const { root, write } = fixture(t);
  write('apps/api/internal/modules/alpha/domain/tagged.go', '//go:build never_enabled\n\npackage domain\nimport _ "database/sql"\n');
  assert.ok(scanGo(root).some(item => item.includes('domain imports outer layer database/sql')));
});

test('require aliases and module.require cannot hide database imports', t => {
  const { root, write } = fixture(t);
  write('apps/client/src/pages/index.vue', '<script setup lang="ts">\nconst load = require; load("mysql2")\n</script>');
  assert.throws(() => scanClient(root), /require alias/);
  write('apps/client/src/pages/index.vue', '<script setup lang="ts">\nmodule.require("mysql2")\n</script>');
  assert.ok(scanClient(root).some(item => item.includes('mysql2')));
});

test('shared mjs modules resolved through TS paths are inspected', t => {
  const { root, write } = fixture(t);
  write('apps/client/tsconfig.json', JSON.stringify({ compilerOptions: { moduleResolution: 'Bundler', module: 'ESNext',
    baseUrl: '.', allowJs: true, paths: { '@nerae/shared/*': ['../../packages/shared/src/*'] } } }));
  write('apps/client/src/pages/index.vue', '<script setup lang="ts">\nimport "@nerae/shared/runtime.mjs"\n</script>');
  write('packages/shared/src/runtime.mjs', 'import "mysql2"\n');
  assert.ok(scanClient(root).some(item => item.includes('mysql2')));
});

test('shared package aliases use their own tsconfig', t => {
  const { root, write } = fixture(t);
  write('apps/client/tsconfig.json', JSON.stringify({ compilerOptions: { moduleResolution: 'Bundler', module: 'ESNext',
    baseUrl: '.', paths: { '@nerae/shared/*': ['../../packages/shared/src/*'] } } }));
  write('packages/shared/tsconfig.json', JSON.stringify({ compilerOptions: { moduleResolution: 'Bundler', module: 'ESNext',
    baseUrl: '.', paths: { '@/*': ['src/*'] } } }));
  write('apps/client/src/pages/index.vue', '<script setup lang="ts">\nimport "@nerae/shared/runtime"\n</script>');
  write('packages/shared/src/runtime.ts', 'import "@/store"\n');
  write('packages/shared/src/store.ts', 'import "mysql2"\n');
  assert.ok(scanClient(root).some(item => item.includes('mysql2')));
});


test('cross-module storage and unknown layers cannot bypass the boundary', t => {
  const { root, write } = fixture(t);
  write('apps/api/internal/modules/alpha/domain/entity.go', 'package domain\nimport _ "nerae/api/internal/modules/beta/storage"\n');
  write('apps/api/internal/modules/beta/storage/store.go', 'package storage\n');
  const failures = scanGo(root);
  assert.ok(failures.some(item => item.includes("another module's storage or unrecognized layer")));
  assert.ok(failures.some(item => item.includes('unrecognized module layer')));
});

test('Vite glob module graphs fail closed before following uninspected dependencies', t => {
  const { root, write } = fixture(t);
  write('apps/client/src/pages/index.vue', '<script setup lang="ts">\nimport.meta.glob("../services/*.ts", { eager: true })\n</script>');
  write('apps/client/src/services/store.ts', 'import "mysql2"\n');
  assert.throws(() => scanClient(root), /dynamic import.meta module graph/);
  write('apps/client/src/pages/index.vue', '<script setup lang="ts">\nimport.meta["glob"]("../services/*.ts")\n</script>');
  assert.throws(() => scanClient(root), /dynamic import.meta module graph/);
});

test('ambient require declarations and safe literal requires are accepted', t => {
  const { root, write } = fixture(t);
  write('apps/client/src/pages/index.vue', '<script setup lang="ts">\ndeclare const require: (path: string) => unknown; require("../safe")\n</script>');
  write('apps/client/src/safe.ts', 'export {}\n');
  assert.deepEqual(scanClient(root), []);
});


test('a resolved alias outside the scanned source tree fails closed', t => {
  const { root, write } = fixture(t);
  write('apps/client/tsconfig.json', JSON.stringify({ compilerOptions: { moduleResolution: 'Bundler', module: 'ESNext',
    baseUrl: '.', paths: { '@external/*': ['../../external-src/*'] } } }));
  write('apps/client/src/pages/index.vue', '<script setup lang="ts">\nimport "@external/store"\n</script>');
  write('external-src/store.ts', 'import "mysql2"\n');
  assert.throws(() => scanClient(root), /resolved module outside scanned source/);
});

test('unapproved client SDKs fail even if absent from a database blacklist', t => {
  const { root, write } = fixture(t);
  write('apps/client/src/pages/index.vue', '<script setup lang="ts">\nimport "@libsql/client"\n</script>');
  assert.ok(scanClient(root).some(item => item.includes('unapproved client dependency @libsql/client')));
});


test('production Go imports cannot hide inside testdata directories', t => {
  const { root, write } = fixture(t);
  write('apps/api/internal/modules/alpha/domain/entity.go', 'package domain\nimport _ "nerae/api/internal/modules/alpha/domain/testdata/storage"\n');
  write('apps/api/internal/modules/alpha/domain/testdata/storage/db.go', 'package storage\nimport _ "database/sql"\n');
  assert.ok(scanGo(root).some(item => item.includes('imports testdata from production code')));
});

test('worker module graphs fail closed until they are explicitly supported', t => {
  const { root, write } = fixture(t);
  write('apps/client/src/pages/index.vue', '<script setup lang="ts">\nnew Worker(new URL("../workers/db.ts", import.meta.url), { type: "module" })\n</script>');
  write('apps/client/src/workers/db.ts', 'import "mysql2"\n');
  assert.throws(() => scanClient(root), /worker module graph/);
  write('apps/client/src/pages/index.vue', '<script setup lang="ts">\nnavigator.serviceWorker.register("../workers/db.js")\n</script>');
  assert.throws(() => scanClient(root), /worker module graph/);
  write('apps/client/src/pages/index.vue', '<script setup lang="ts">\nuni.createWorker("../workers/db.js")\n</script>');
  assert.throws(() => scanClient(root), /worker module graph/);
});
