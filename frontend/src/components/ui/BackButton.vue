<script setup lang="ts">
import type { RouteLocationRaw } from 'vue-router'
import Button from './Button.vue'

defineProps<{ to: RouteLocationRaw }>()
</script>

<template>
  <Button :to="to" variant="secondary" class="gap-3 px-4" data-back-link>
    <span class="flex h-6 w-6 shrink-0 items-center justify-center rounded-sm bg-primary" aria-hidden="true">
      <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" focusable="false">
        <path
          d="M19 12H5m7-7-7 7 7 7"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
        />
      </svg>
    </span>
    <span><slot /></span>
  </Button>
</template>
