import { createApp } from 'vue'
import App from './App.vue'
import { initializePalette } from './state/palette'
import './styles/tokens.css'
import './styles/base.css'
import './styles/screens.css'
import './styles/round-four.css'
import './styles/round-five.css'

initializePalette()
createApp(App).mount('#app')
