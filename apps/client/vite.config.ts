import { defineConfig } from 'vite'
import uniModule from '@dcloudio/vite-plugin-uni'

// DCloud's CommonJS plugin exposes its factory under `default` when Vite loads
// this config as native ESM. Keep the same config usable by either loader.
const uni = typeof uniModule === 'function'
  ? uniModule
  : (uniModule as unknown as { default: typeof uniModule }).default

export default defineConfig({
  plugins: [uni()],
})
