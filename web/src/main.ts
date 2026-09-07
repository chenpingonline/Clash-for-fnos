import { createApp } from 'vue'
import App from './App.vue'
import './styles.css'

const app = createApp(App)
app.config.errorHandler = (error) => {
  console.error('[Clash for fnOS]', error)
}
app.mount('#app')
