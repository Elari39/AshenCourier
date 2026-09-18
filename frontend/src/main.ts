// 字体：@fontsource 打包进产物，离线可用，不依赖任何 CDN
import '@fontsource/inter/400.css'
import '@fontsource/inter/500.css'
import '@fontsource/cormorant-garamond/400.css'
import '@fontsource/cormorant-garamond/500.css'

import '@/assets/main.css'

import { createApp } from 'vue'

import App from '@/App.vue'
import { router } from '@/router'

createApp(App).use(router).mount('#app')
