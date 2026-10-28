import { createRequire } from 'node:module';
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const clientRequire = createRequire(path.join(root, 'apps/client/package.json'));
const ts = clientRequire('typescript');
const vueRequire = createRequire(clientRequire.resolve('vue'));
const { parse: parseVue } = vueRequire('@vue/compiler-sfc');
const extensions = new Set(['.ts', '.tsx', '.js', '.jsx', '.vue']);
const settings = { ...ts.getDefaultFormatCodeSettings(), indentSize: 2, tabSize: 2, newLineCharacter: '\n' };

function walk(directory) {
  if (!existsSync(directory)) return [];
  return readdirSync(directory, { withFileTypes: true }).flatMap(entry => {
    if (entry.name.startsWith('.') || ['node_modules', 'dist'].includes(entry.name)) return [];
    const file = path.join(directory, entry.name);
    if (entry.isSymbolicLink()) throw new Error(`refusing source symlink: ${file}`);
    if (entry.isDirectory()) return walk(file);
    return entry.isFile() && extensions.has(path.extname(file)) ? [file] : [];
  }).sort();
}

function editsFor(file, content) {
  const host = {
    getScriptFileNames: () => [file],
    getScriptVersion: () => '0',
    getScriptSnapshot: name => name === file ? ts.ScriptSnapshot.fromString(content) : undefined,
    getCurrentDirectory: () => root,
    getCompilationSettings: () => ({}),
    getDefaultLibFileName: () => 'lib.d.ts',
    fileExists: name => name === file,
    readFile: name => name === file ? content : undefined,
    readDirectory: () => [],
  };
  const service = ts.createLanguageService(host);
  try { return service.getFormattingEditsForDocument(file, settings); }
  finally { service.dispose(); }
}

export function formatIssues(file, content) {
  const issues = [];
  if (content.includes('\r')) issues.push('CR characters');
  if (content && !content.endsWith('\n')) issues.push('missing final newline');
  if (/[\t ]+$/m.test(content)) issues.push('trailing whitespace');
  let chunks = [{ file, content }];
  if (file.endsWith('.vue')) {
    const result = parseVue(content, { filename: file });
    if (result.errors.length) throw new Error(`invalid Vue file: ${file}`);
    chunks = [result.descriptor.script, result.descriptor.scriptSetup].filter(Boolean).map((block, i) => ({
      file: `${file}.${i}.${['tsx', 'jsx'].includes(block.lang) ? 'tsx' : 'ts'}`,
      content: block.content,
    }));
  }
  for (const chunk of chunks) {
    const edits = editsFor(chunk.file, chunk.content);
    if (edits.length) issues.push(`TypeScript formatting: ${edits.length} edit(s)`);
  }
  return issues;
}

export function check(rootPath = root) {
  const files = [
    ...walk(path.join(rootPath, 'apps/client/src')),
    ...walk(path.join(rootPath, 'apps/admin/src')),
    ...walk(path.join(rootPath, 'packages')),
    ...['apps/client/vite.config.ts', 'apps/admin/vite.config.ts'].map(file => path.join(rootPath, file)).filter(existsSync),
  ];
  if (!files.length) throw new Error('no TS/JS/Vue source files found');
  const issues = files.flatMap(file => formatIssues(file, readFileSync(file, 'utf8')).map(issue => `${path.relative(rootPath, file)}: ${issue}`));
  if (issues.length) {
    for (const issue of issues) console.error(`format: ${issue}`);
    return 1;
  }
  console.log(`TS/JS and Vue script formatting: ${files.length} files checked`);
  return 0;
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  try { process.exitCode = check(); }
  catch (error) { console.error(`format: ${error.message}`); process.exitCode = 1; }
}
