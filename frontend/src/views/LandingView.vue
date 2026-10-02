<script setup lang="ts">
import { ref } from 'vue'
import type { Link } from '@/api/types'
import ResultCard from '@/components/ResultCard.vue'
import ShortenForm from '@/components/ShortenForm.vue'
import Card from '@/components/ui/Card.vue'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import { shortBase } from '@/utils/shortlink'

const latest = ref<{ link: Link; manageKey?: string } | null>(null)
const example = `${shortBase()}/hello-miku`
const features = [
  {
    n: '01',
    title: '粘贴，生成，分享。',
    text: '把长地址变成容易记住的短链接。加上自己的短码，让分享更有辨识度。',
  },
  {
    n: '02',
    title: '你的链接，你来掌控。',
    text: '为链接设置口令与有效期，随时修改目标地址。匿名也能创建，登录后可认领管理。',
  },
  { n: '03', title: '每次分享，都有回响。', text: '查看点击趋势、访问来源与设备分布，了解链接被怎样打开。' },
]
</script>

<template>
  <section class="hero-grid neo-hero">
    <div class="container-page">
      <div class="neo-hero-layout">
        <div class="neo-hero-copy">
          <Badge variant="accent">SHORT LINKS / BIG ENERGY</Badge>
          <h1 class="neo-headline" tabindex="-1">长链接？<br /><span>短着来。</span></h1>
          <p class="mt-7 max-w-md text-lg leading-relaxed text-ink">
            把复杂留给地址。<br />把简单，留给每一次分享。
          </p>
          <div class="mt-8 flex flex-wrap items-center gap-4">
            <span class="neo-sticker">粘贴 → 生成 → 分享 ↗</span>
            <span class="font-mono text-xs font-bold">NO ACCOUNT NEEDED</span>
          </div>
          <div class="neo-example">
            <span class="text-xs font-bold">一个小小的改变 / 示例</span>
            <p class="mt-3 break-anywhere font-mono text-xs line-through opacity-60">
              example.com/articles/a-wonderful-story?from=friends
            </p>
            <p class="mt-2 break-anywhere font-mono text-sm font-bold">{{ example }}</p>
          </div>
        </div>
        <div class="min-w-0">
          <div class="neo-workbench">
            <div class="neo-window-bar">
              <span>01 / LINK MAKER</span><span aria-hidden="true">● ● ●</span>
            </div>
            <div class="p-5 md:p-7"><ShortenForm @created="latest = $event" /></div>
          </div>
          <div v-if="latest" class="mt-7">
            <ResultCard :link="latest.link" :manage-key="latest.manageKey" />
          </div>
          <div v-else class="neo-note">
            <span class="neo-note-symbol" aria-hidden="true">↗</span>
            <div>
              <p class="font-bold">你的下一次分享，从这里开始。</p>
              <p class="mt-1 text-xs leading-relaxed">默认 24 小时有效。需要更久？在高级选项里自由设置。</p>
            </div>
          </div>
        </div>
      </div>
    </div>
  </section>
  <div class="neo-ribbon" aria-hidden="true">
    <span>LESS LINK</span><span>✳</span><span>MORE SHARING</span><span>✳</span><span>MAKE IT SHORT</span>
  </div>
  <section class="container-page py-14 md:py-20">
    <p class="eyebrow">SMALL TOOL. REAL CONTROL.</p>
    <h2 class="display-md mt-5">短小，但不简单。</h2>
    <div class="mt-9 grid gap-7 md:grid-cols-3">
      <Card v-for="feature in features" :key="feature.n" variant="feature" class="neo-feature">
        <span class="neo-feature-number">{{ feature.n }}</span>
        <h3 class="title-lg mt-7">{{ feature.title }}</h3>
        <p class="mt-3 text-sm leading-relaxed">{{ feature.text }}</p>
      </Card>
    </div>
    <Card variant="accent" class="mt-12 flex flex-wrap items-center justify-between gap-7">
      <div>
        <p class="eyebrow mb-4">YOUR LINKS. YOUR SPACE.</p>
        <h2 class="display-sm">好链接，值得有个收纳处。</h2>
        <p class="mt-3">登录后集中管理，换个设备也能继续。</p>
      </div>
      <Button :to="{ name: 'dashboard' }" variant="secondary">打开我的链接 ↗</Button>
    </Card>
  </section>
</template>
