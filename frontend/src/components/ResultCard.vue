<script setup lang="ts">
/**
 * 创建成功后的结果卡。
 *
 * 深色 code-window 承载短链本身 —— DESIGN.md 认为「深色面板就是产品 chrome」，
 * 短链在这里就是产品实物，不是营销插图。
 */
import { RouterLink } from 'vue-router'

import type { Link } from '@/api/types'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import CodeWindow from '@/components/ui/CodeWindow.vue'
import CopyButton from '@/components/ui/CopyButton.vue'
import { useMaskedSecret } from '@/composables/useMaskedSecret'
import { describeExpiry, formatDateTime, truncateMiddle } from '@/utils/format'

const props = defineProps<{
  link: Link
  /** 仅在匿名创建时存在，且只在这一次响应里给到。 */
  manageKey?: string
}>()

/**
 * 管理密钥默认打码：肩窥风险比便利性更值得防。
 *
 * 「换了一条短链就收起」这一条交给 useMaskedSecret —— 结果卡是**被复用**的
 * （落地页与列表页都只把 `latest` 换成新 payload，同一位置的实例不重建），
 * 少了这一步，第一次点过「显示」之后，下一条短链的密钥会默认明文摊在屏幕上。
 * 单独抽出来是为了能被单测覆盖：细节见那个文件。
 */
const { revealed: revealKey, toggle: toggleKey } = useMaskedSecret(() => props.link.short_code)
</script>

<template>
  <Card variant="dark">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <Badge variant="coral">已生成</Badge>
      <span class="text-[13px] text-on-dark-soft">{{ describeExpiry(link.expires_at) }}</span>
    </div>

    <!-- 短链本体：等宽字体 + 深色内嵌面板 -->
    <CodeWindow class="mt-5 flex flex-wrap items-center gap-3">
      <a
        :href="link.short_url"
        target="_blank"
        rel="noopener noreferrer"
        class="min-w-0 flex-1 break-anywhere text-on-dark underline-offset-4 hover:underline"
      >
        {{ link.short_url }}
      </a>
      <div class="flex items-center gap-2">
        <CopyButton :value="link.short_url" variant="secondary-dark" size="sm" />
        <Button variant="secondary-dark" size="sm" :href="link.short_url">打开</Button>
      </div>
    </CodeWindow>

    <!-- 目标地址 -->
    <p class="mt-4 text-[13px] text-on-dark-soft">
      指向
      <span class="break-anywhere ml-1 text-on-dark">{{ truncateMiddle(link.target_url, 46, 22) }}</span>
    </p>

    <!-- 一次性管理密钥 -->
    <div v-if="manageKey" class="mt-5 rounded-md border border-surface-dark-elevated p-4">
      <p class="text-[13px] leading-[1.55] text-on-dark-soft">
        这是<strong class="font-medium text-on-dark">一次性管理密钥</strong>，只显示这一次。
        没有它就无法再管理这条匿名短链，请立刻保存；登录后也可以用它把链接「认领」到账号下。
      </p>
      <div class="mt-3 flex flex-wrap items-center gap-2">
        <code class="min-w-0 flex-1 break-anywhere rounded-sm bg-surface-dark-soft px-2.5 py-1.5 text-[12px] text-on-dark">
          {{ revealKey ? manageKey : '•••••••••••••••••••••••••••••••••••••••••••' }}
        </code>
        <Button variant="secondary-dark" size="sm" @click="toggleKey">
          {{ revealKey ? '隐藏' : '显示' }}
        </Button>
        <CopyButton
          :value="manageKey"
          variant="secondary-dark"
          size="sm"
          success-message="管理密钥已复制"
        />
      </div>
    </div>

    <div class="mt-5 flex flex-wrap items-center gap-4 text-[13px]">
      <RouterLink :to="{ name: 'link-detail', params: { code: link.short_code } }" class="text-link">
        查看点击统计
      </RouterLink>
      <span class="text-on-dark-soft">短码 <code class="text-on-dark">{{ link.short_code }}</code></span>
      <span class="text-on-dark-soft">创建于 {{ formatDateTime(link.created_at) }}</span>
    </div>
  </Card>
</template>
