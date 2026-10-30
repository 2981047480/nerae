// App JavaScriptCore and some mini-program runtimes may not provide URL.
import 'core-js/actual/url'
import { createSSRApp } from 'vue'
import App from './App.vue'

export function createApp() {
  const app = createSSRApp(App)
  return { app }
}
