import { createApp, h } from 'vue'

const mountPoint = document.querySelector('#app')
if (!mountPoint) {
  throw new Error('Admin shell mount point is missing')
}

createApp({
  render: () => h('main', { 'aria-label': 'Nerae admin shell' }),
}).mount(mountPoint)
