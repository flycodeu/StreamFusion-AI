import { createApp } from 'vue'
import App from './App.vue'
import { router } from './router'
import { systemConfig } from './config/system'
import 'element-plus/dist/index.css'
import './style.css'

document.title = systemConfig.name
const favicon = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
if (favicon) favicon.href = systemConfig.logo

createApp(App).use(router).mount('#app')
