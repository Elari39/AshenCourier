<script setup lang="ts">
// 应用根组件只做一件事：把路由树渲染出来。
// 版式（顶栏 / 页脚 / toast）由 layouts/DefaultLayout.vue 承担。
import { RouterView } from 'vue-router'
</script>

<template>
  <RouterView />
</template>
