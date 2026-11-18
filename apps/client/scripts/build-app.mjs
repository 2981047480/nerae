import { spawn } from 'node:child_process'
import { lstatSync, rmSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

// The pinned compiler prints this and process.exit(0) when UTS fails to load.
const missingUts = 'Cannot find module: @dcloudio/uni-uts-v1'
const clientRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')

function artifactPath(env) {
  if (env.UNI_OUTPUT_DIR) return path.resolve(clientRoot, env.UNI_OUTPUT_DIR, 'app-service.js')
  return path.join(clientRoot, 'dist', 'build', 'app', 'app-service.js')
}

function artifactReady(file) {
  try {
    const info = lstatSync(file)
    return info.isFile() && info.size > 0
  } catch (error) {
    if (error && error.code === 'ENOENT') return false
    throw error
  }
}

function removePreviousArtifact(file) {
  let info
  try {
    info = lstatSync(file)
  } catch (error) {
    if (error && error.code === 'ENOENT') return
    throw error
  }
  if (info.isDirectory()) {
    console.error(`app resource build failed: ${file} is a directory`)
    process.exit(1)
  }
  rmSync(file)
}

function finish(compilerExit, log, artifact) {
  const missingModule = log.includes(missingUts)
  const ready = artifactReady(artifact)
  const compilerCode = typeof compilerExit === 'number' ? compilerExit : 1
  const code = compilerCode !== 0 || missingModule || !ready ? (compilerCode !== 0 ? compilerCode : 1) : 0
  if (code !== 0) {
    if (compilerCode !== 0) console.error(`app resource build failed: uni build -p app exited ${compilerCode}`)
    if (missingModule) console.error(`app resource build failed: compiler log contains "${missingUts}"`)
    if (!ready) console.error(`app resource build failed: missing nonempty ${artifact}`)
  }
  process.exit(code)
}

const artifact = artifactPath(process.env)
removePreviousArtifact(artifact)

const child = spawn('uni', ['build', '-p', 'app'], {
  cwd: clientRoot,
  env: process.env,
  stdio: ['inherit', 'pipe', 'pipe'],
})

let log = ''
let settled = false
function settle(report) {
  if (settled) return
  settled = true
  report()
}

function forward(stream, write) {
  stream.setEncoding('utf8')
  stream.on('data', (chunk) => {
    log += chunk
    write(chunk)
  })
}
forward(child.stdout, (text) => process.stdout.write(text))
forward(child.stderr, (text) => process.stderr.write(text))

child.on('error', (error) => {
  settle(() => {
    console.error(`app resource build failed: cannot run uni build -p app: ${error.message}`)
    process.exit(1)
  })
})

child.on('close', (code) => {
  settle(() => finish(code, log, artifact))
})
