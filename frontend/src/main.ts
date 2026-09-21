// 字体：@fontsource 打包进产物，离线可用，不依赖任何 CDN。
//
// ⚠️ 必须带 `latin-` 子集名。不带子集名的 `400.css` 会把 cyrillic / cyrillic-ext /
// greek / greek-ext / vietnamese / latin-ext 全部子集一起打进产物 —— 实测 48 个字体
// 文件、782KB，而这套中英界面一个都用不到（中文本来就由 font-sans 里的
// PingFang SC / Microsoft YaHei 兜底，Inter 只管拉丁字符）。
// 只引 latin 之后是 8 个文件、约 220KB。
import '@fontsource/inter/latin-400.css'
import '@fontsource/inter/latin-500.css'
import '@fontsource/cormorant-garamond/latin-400.css'
import '@fontsource/cormorant-garamond/latin-500.css'

import '@/assets/main.css'

import { createApp } from 'vue'

import App from '@/App.vue'
import { router } from '@/router'

createApp(App).use(router).mount('#app')
