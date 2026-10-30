import { existsSync } from 'node:fs'
import { dirname, extname, resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { registerHooks } from 'node:module'

registerHooks({
  resolve(specifier, context, nextResolve) {
    try { return nextResolve(specifier, context) }
    catch (error) {
      if (!specifier.startsWith('.') || !context.parentURL?.startsWith('file:')) throw error
      const parent = fileURLToPath(context.parentURL)
      if (extname(specifier)) throw error
      for (const extension of ['.ts', '.mjs', '.js']) {
        const candidate = resolve(dirname(parent), `${specifier}${extension}`)
        if (existsSync(candidate)) return nextResolve(pathToFileURL(candidate).href, context)
      }
      throw error
    }
  },
})
