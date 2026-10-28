import { createRequire } from 'node:module';
import { spawnSync } from 'node:child_process';
import { existsSync, readdirSync, readFileSync, realpathSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const toolRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const appRequire = createRequire(path.join(toolRoot, 'apps/client/package.json'));
const ts = appRequire('typescript');
const vueRequire = createRequire(appRequire.resolve('vue'));
const { parse: parseVue } = vueRequire('@vue/compiler-sfc');
const codeExtensions = new Set(['.ts', '.tsx', '.js', '.jsx', '.vue', '.mjs', '.cjs', '.mts', '.cts']);
// View graphs may depend on these UI/runtime packages; new packages require review.
const allowedClientPackages = ['vue', 'vue-i18n', '@dcloudio/uni-app', '@dcloudio/uni-components'];
const persistencePackages = ['database/sql', 'github.com/go-sql-driver/mysql', 'github.com/redis/go-redis',
  'gorm.io/gorm', 'github.com/jackc/pgx'];

function isPackage(specifier, base) {
  return specifier === base || specifier.startsWith(`${base}/`);
}

function walkFiles(directory) {
  if (!existsSync(directory)) return [];
  const result = [];
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    if (entry.name === 'node_modules' || entry.name === 'dist' || entry.name.startsWith('.')) continue;
    const child = path.join(directory, entry.name);
    if (entry.isSymbolicLink()) throw new Error(`refusing symlink in source tree: ${child}`);
    if (entry.isDirectory()) result.push(...walkFiles(child));
    else if (entry.isFile() && codeExtensions.has(path.extname(entry.name))) result.push(child);
  }
  return result.sort();
}

function parseImports(file) {
  const text = readFileSync(file, 'utf8');
  let chunks = [{ content: text, kind: file.endsWith('.tsx') || file.endsWith('.jsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS }];
  const imports = [];
  if (file.endsWith('.vue')) {
    const result = parseVue(text, { filename: file });
    if (result.errors.length) throw new Error(`invalid Vue file: ${file}`);
    chunks = [result.descriptor.script, result.descriptor.scriptSetup].filter(Boolean).map(block => {
      if (block.src) imports.push(block.src);
      return { content: block.content, kind: ['tsx', 'jsx'].includes(block.lang) ? ts.ScriptKind.TSX : ts.ScriptKind.TS };
    });
  }
  for (const [index, chunk] of chunks.entries()) {
    const source = ts.createSourceFile(`${file}.${index}.ts`, chunk.content, ts.ScriptTarget.Latest, true, chunk.kind);
    if (source.parseDiagnostics.length) throw new Error(`cannot parse imports in ${file}`);
    function visit(node) {
      if ((ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) && node.moduleSpecifier) {
        if (ts.isStringLiteralLike(node.moduleSpecifier)) imports.push(node.moduleSpecifier.text);
        else throw new Error(`nonliteral module import in ${file}`);
      } else if (ts.isImportEqualsDeclaration(node) && ts.isExternalModuleReference(node.moduleReference)) {
        const expression = node.moduleReference.expression;
        if (expression && ts.isStringLiteralLike(expression)) imports.push(expression.text);
        else throw new Error(`nonliteral module import in ${file}`);
      } else if (ts.isCallExpression(node) && (node.expression.kind === ts.SyntaxKind.ImportKeyword ||
                 (ts.isIdentifier(node.expression) && node.expression.text === 'require') ||
                 (ts.isPropertyAccessExpression(node.expression) && ts.isIdentifier(node.expression.expression) &&
                  node.expression.expression.text === 'module' && node.expression.name.text === 'require'))) {
        if (node.arguments.length === 1 && ts.isStringLiteralLike(node.arguments[0])) imports.push(node.arguments[0].text);
        else throw new Error(`dynamic module import cannot be checked in ${file}`);
      }
      const importMeta = expression => ts.isMetaProperty(expression) &&
        expression.keywordToken === ts.SyntaxKind.ImportKeyword && expression.name.text === 'meta';
      if ((ts.isPropertyAccessExpression(node) && importMeta(node.expression) &&
           ['glob', 'globEager'].includes(node.name.text)) ||
          (ts.isElementAccessExpression(node) && importMeta(node.expression))) {
        throw new Error(`dynamic import.meta module graph cannot be checked in ${file}`);
      }
      const workerNames = ['Worker', 'SharedWorker', 'serviceWorker', 'createWorker'];
      const isTypeUse = parent => ts.isTypeReferenceNode(parent) || ts.isTypeQueryNode(parent);
      if ((ts.isIdentifier(node) && ['Worker', 'SharedWorker'].includes(node.text) && !isTypeUse(node.parent)) ||
          (ts.isPropertyAccessExpression(node) && workerNames.includes(node.name.text)) ||
          (ts.isElementAccessExpression(node) && node.argumentExpression &&
           ts.isStringLiteralLike(node.argumentExpression) && workerNames.includes(node.argumentExpression.text))) {
        throw new Error(`worker module graph cannot be checked in ${file}`);
      }
      if (ts.isIdentifier(node) && node.text === 'require') {
        const parent = node.parent;
        const directCall = ts.isCallExpression(parent) && parent.expression === node;
        const moduleCall = ts.isPropertyAccessExpression(parent) && parent.name === node &&
          ts.isIdentifier(parent.expression) && parent.expression.text === 'module' &&
          ts.isCallExpression(parent.parent) && parent.parent.expression === parent;
        const ambientDeclaration = ts.isVariableDeclaration(parent) && parent.name === node &&
          !parent.initializer && ts.isVariableDeclarationList(parent.parent) &&
          ts.isVariableStatement(parent.parent.parent) &&
          parent.parent.parent.modifiers?.some(modifier => modifier.kind === ts.SyntaxKind.DeclareKeyword);
        if (!directCall && !moduleCall && !ambientDeclaration) throw new Error(`require alias cannot be checked in ${file}`);
      }
      if (ts.isElementAccessExpression(node) && node.argumentExpression &&
          ts.isStringLiteralLike(node.argumentExpression) && node.argumentExpression.text === 'require') {
        throw new Error(`computed require cannot be checked in ${file}`);
      }
      if ((ts.isCallExpression(node) || ts.isNewExpression(node)) && ts.isIdentifier(node.expression) &&
          ['eval', 'Function'].includes(node.expression.text)) {
        throw new Error(`dynamic code evaluation cannot be checked in ${file}`);
      }
      ts.forEachChild(node, visit);
    }
    visit(source);
  }
  return imports;
}

function candidates(base) {
  return [base, ...['.ts', '.tsx', '.js', '.jsx', '.vue', '.mjs', '.cjs', '.mts', '.cts'].map(ext => base + ext),
    ...['index.ts', 'index.tsx', 'index.js', 'index.vue', 'index.mjs', 'index.cjs', 'index.mts', 'index.cts'].map(name => path.join(base, name))];
}

function resolveLocal(specifier, file, appRoot, root, knownFiles, fallbackOptions) {
  let directory = path.dirname(file);
  let options = fallbackOptions;
  while (directory === root || directory.startsWith(root + path.sep)) {
    const configPath = path.join(directory, 'tsconfig.json');
    if (existsSync(configPath)) {
      const config = ts.readConfigFile(configPath, ts.sys.readFile);
      if (config.error) throw new Error(`cannot read TypeScript config for ${directory}`);
      const parsed = ts.parseJsonConfigFileContent(config.config, ts.sys, directory);
      if (parsed.errors.length) throw new Error(`cannot parse TypeScript config for ${directory}`);
      options = parsed.options;
      break;
    }
    if (directory === root) break;
    directory = path.dirname(directory);
  }
  const resolved = ts.resolveModuleName(specifier, file, options, ts.sys).resolvedModule?.resolvedFileName;
  if (resolved) {
    const absolute = path.resolve(resolved);
    const real = existsSync(absolute) ? realpathSync(absolute) : absolute;
    if (knownFiles.has(real)) return real;
    if (knownFiles.has(absolute)) return absolute;
    const dependencyPath = absolute.split(path.sep).includes('node_modules');
    if (!dependencyPath) throw new Error(`resolved module outside scanned source: ${specifier} in ${file}`);
  }
  let base;
  const isAppSource = file.startsWith(path.join(appRoot, 'src') + path.sep);
  if (specifier.startsWith('@/') && isAppSource) base = path.join(appRoot, 'src', specifier.slice(2));
  else if (specifier.startsWith('.')) base = path.resolve(path.dirname(file), specifier);
  if (base) return candidates(base).find(candidate => knownFiles.has(path.resolve(candidate)));
  return undefined;
}

export function scanClient(root) {
  const failures = [];
  const roots = [path.join(root, 'apps/client/src'), path.join(root, 'apps/admin/src'), path.join(root, 'packages')];
  const files = roots.flatMap(walkFiles).map(file => path.resolve(file));
  if (!files.length) throw new Error('no client or shared source files found');
  const known = new Set(files);
  const imports = new Map(files.map(file => [file, parseImports(file)]));
  const appFor = file => file.startsWith(path.join(root, 'apps/client') + path.sep) ? path.join(root, 'apps/client') : path.join(root, 'apps/admin');
  const views = files.filter(file => file.endsWith('.vue') || file.includes(`${path.sep}pages${path.sep}`) ||
    file === path.join(root, 'apps/client/src/main.ts') || file === path.join(root, 'apps/admin/src/main.ts'));
  if (!views.length) throw new Error('no client views found');
  for (const view of views) {
    const app = appFor(view);
    const config = ts.readConfigFile(path.join(app, 'tsconfig.json'), ts.sys.readFile);
    if (config.error) throw new Error(`cannot read TypeScript config for ${app}`);
    const parsed = ts.parseJsonConfigFileContent(config.config, ts.sys, app);
    const visited = new Set();
    const visit = file => {
      if (visited.has(file)) return;
      visited.add(file);
      for (const specifier of imports.get(file) || []) {
        const target = resolveLocal(specifier, file, app, root, known, parsed.options);
        if (target) visit(target);
        else if (specifier.startsWith('.') || specifier.startsWith('@/') || specifier.startsWith('@nerae/')) {
          failures.push(`${path.relative(root, file)} has unresolved local import ${specifier}`);
        } else if (!allowedClientPackages.some(pkg => isPackage(specifier, pkg))) {
          failures.push(`${path.relative(root, view)} reaches unapproved client dependency ${specifier} via ${path.relative(root, file)}`);
        }
      }
    };
    visit(view);
  }
  return failures;
}

function runGo(go, cwd, args) {
  const result = spawnSync(go, args, { cwd, encoding: 'utf8', env: { ...process.env, GOTOOLCHAIN: 'local' } });
  if (result.error || result.status !== 0) throw new Error(`go ${args[0]} failed: ${result.error?.message || result.stderr.trim()}`);
  return result.stdout.trim();
}

function goModuleParts(module, value) {
  const prefix = `${module}/internal/modules/`;
  if (!value.startsWith(prefix)) return null;
  const [owner, ...parts] = value.slice(prefix.length).split('/');
  return { owner, parts };
}

export function scanGo(root, go = process.env.NERAE_GO || 'go') {
  const cwd = path.join(root, 'apps/api');
  const module = runGo(go, cwd, ['list', '-m', '-f', '{{.Path}}']);
  if (!module) throw new Error('Go module path is empty');
  const output = runGo(go, cwd, ['run', path.join(toolRoot, 'scripts/quality/go_imports.go'), cwd]);
  const entries = JSON.parse(output);
  if (!Array.isArray(entries) || !entries.length) throw new Error('no Go source files found');
  const failures = [];
  for (const entry of entries) {
    const source = entry.directory === '.' ? module : `${module}/${entry.directory}`;
    const dependencies = entry.imports;
    for (const dependency of dependencies.filter(Boolean)) {
      if (dependency.split('/').includes('testdata')) {
        failures.push(`${source} imports testdata from production code: ${dependency}`);
      }
    }
    const from = goModuleParts(module, source);
    if (!from) continue;
    const layer = from.parts[0];
    const knownLayers = ['domain', 'application', 'transport', 'handler', 'repository',
      'infrastructure', 'persistence', 'api', 'contract'];
    if (!knownLayers.includes(layer)) {
      failures.push(`${source} uses an unrecognized module layer`);
      continue;
    }
    for (const dependency of dependencies.filter(Boolean)) {
      const to = goModuleParts(module, dependency);
      const lowerLayer = to && ['transport', 'handler', 'repository', 'infrastructure', 'persistence', 'api'].includes(to.parts[0]);
      if (layer === 'domain' && (persistencePackages.some(pkg => isPackage(dependency, pkg)) ||
          isPackage(dependency, 'github.com/gin-gonic/gin') || (to && to.owner === from.owner &&
          ['application', 'transport', 'handler', 'repository', 'infrastructure', 'persistence', 'api'].includes(to.parts[0])))) {
        failures.push(`${source} domain imports outer layer ${dependency}`);
      } else if (layer === 'application' && to && to.owner === from.owner && lowerLayer) {
        failures.push(`${source} application imports outer layer ${dependency}`);
      } else if (['transport', 'handler'].includes(layer) && persistencePackages.some(pkg => isPackage(dependency, pkg))) {
        failures.push(`${source} handler imports persistence dependency ${dependency}`);
      } else if (to && to.owner !== from.owner && !['domain', 'application', 'api', 'contract'].includes(to.parts[0])) {
        failures.push(`${source} imports another module's storage or unrecognized layer ${dependency}`);
      } else if (to && to.owner === from.owner && !knownLayers.includes(to.parts[0])) {
        failures.push(`${source} imports an unrecognized module layer ${dependency}`);
      }
    }
  }
  return failures;
}

export function main(root = toolRoot) {
  const failures = [...scanGo(root), ...scanClient(root)];
  if (failures.length) {
    for (const failure of failures) console.error(`boundary: ${failure}`);
    return 1;
  }
  console.log('Boundary import checks passed; SQL ownership and runtime authorization require separate tests.');
  return 0;
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  try { process.exitCode = main(process.argv[2] ? path.resolve(process.argv[2]) : toolRoot); }
  catch (error) { console.error(`boundary: ${error.message}`); process.exitCode = 1; }
}
