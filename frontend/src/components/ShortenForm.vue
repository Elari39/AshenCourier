<script setup lang="ts">
import { computed, nextTick, ref, useId, watch } from 'vue'
import { ApiError, linksApi } from '@/api/client'
import type { Link } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import { useAuth } from '@/composables/useAuth'
import { creationExpiry, type ExpiryChoice } from '@/utils/expiry'
import { splitTags } from '@/utils/tags'

defineProps<{ compact?: boolean }>()

const emit = defineEmits<{
  (event: 'created', payload: { link: Link; manageKey?: string }): void
}>()
const { rememberManageKey, isAuthenticated } = useAuth()
const targetURL = ref('')
const customCode = ref('')
const title = ref('')
const tags = ref('')
const expiresAt = ref('')
const expiryChoice = ref<ExpiryChoice>('24h')
const password = ref('')
const passwordEnabled = ref(false)
const passwordVisible = ref(false)
const advancedOpen = ref(false)
const advancedId = useId()
const form = ref<HTMLFormElement | null>(null)
const submitting = ref(false)
const formError = ref('')
const fieldErrors = ref<Record<string, string>>({})
const canSubmit = computed(() => targetURL.value.trim().length > 0 && !submitting.value)
const expiryOptions = [
  { value: '1h', label: '1 小时' },
  { value: '24h', label: '24 小时（默认）' },
  { value: '7d', label: '7 天' },
  { value: '30d', label: '30 天' },
  { value: 'permanent', label: '永久有效' },
  { value: 'custom', label: '自定义时间' },
]
const expirySummary = computed(() => {
  if (expiryChoice.value === 'permanent') return '永久有效'
  if (expiryChoice.value === 'custom')
    return expiresAt.value ? `到期：${expiresAt.value.replace('T', ' ')}` : '待设置到期时间'
  return `${expiryOptions.find((option) => option.value === expiryChoice.value)?.label.replace('（默认）', '')}后过期`
})
const parsedTags = computed(() => splitTags(tags.value))
watch(expiryChoice, () => {
  delete fieldErrors.value.expires_at
})
watch(passwordEnabled, () => {
  passwordVisible.value = false
  delete fieldErrors.value.password
})

// Remove stale validation messages as the corresponding input is corrected.
watch(password, () => {
  delete fieldErrors.value.password
})
watch(expiresAt, () => {
  delete fieldErrors.value.expires_at
})
watch(customCode, () => {
  delete fieldErrors.value.custom_code
})
watch(tags, () => {
  delete fieldErrors.value.tags
})
watch(title, () => {
  delete fieldErrors.value.title
})
watch(targetURL, () => {
  delete fieldErrors.value.target_url
})

function resetOptions(): void {
  customCode.value = ''
  title.value = ''
  tags.value = ''
  expiresAt.value = ''
  expiryChoice.value = '24h'
  password.value = ''
  passwordEnabled.value = false
  passwordVisible.value = false
  fieldErrors.value = {}
  formError.value = ''
}
async function showError(field: string, message: string): Promise<void> {
  fieldErrors.value = { [field]: message }
  if (field !== 'target_url') advancedOpen.value = true
  await nextTick()
  form.value?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus()
}
async function submit(): Promise<void> {
  if (!canSubmit.value) return
  formError.value = ''
  fieldErrors.value = {}
  let expiry: string | undefined
  try {
    // Start relative durations when submitted, not when the page was opened.
    expiry = creationExpiry(expiryChoice.value, expiresAt.value)
  } catch (cause) {
    await showError('expires_at', (cause as Error).message)
    return
  }
  if (customCode.value && !/^[A-Za-z0-9_-]{3,32}$/.test(customCode.value.trim())) {
    await showError('custom_code', '请输入 3–32 位字母、数字、连字符或下划线')
    return
  }
  if (passwordEnabled.value) {
    const length = new TextEncoder().encode(password.value).length
    if (length < 8 || length > 72 || !password.value.trim()) {
      await showError('password', '访问口令需为 8–72 字节，不能全部为空格')
      return
    }
  }
  if (parsedTags.value.length > 10 || parsedTags.value.some((tag) => [...tag].length > 32)) {
    await showError('tags', '最多添加 10 个标签，每个标签不超过 32 个字符')
    return
  }
  submitting.value = true
  try {
    const result = await linksApi.create({
      target_url: targetURL.value.trim(),
      ...(customCode.value.trim() ? { custom_code: customCode.value.trim() } : {}),
      ...(title.value.trim() ? { title: title.value.trim() } : {}),
      ...(parsedTags.value.length ? { tags: parsedTags.value } : {}),
      ...(expiry ? { expires_at: expiry } : {}),
      ...(passwordEnabled.value ? { password: password.value } : {}),
    })
    if (result.manage_key) rememberManageKey(result.link.short_code, result.manage_key)
    emit('created', { link: result.link, manageKey: result.manage_key })
    targetURL.value = ''
    resetOptions()
    advancedOpen.value = false
  } catch (cause) {
    if (cause instanceof ApiError) {
      if (cause.field) {
        submitting.value = false
        await showError(cause.field, cause.message)
      } else formError.value = cause.friendly
    } else formError.value = '生成失败，请稍后重试。已填写的内容会保留。'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <form ref="form" class="w-full min-w-0" novalidate @submit.prevent="submit">
    <fieldset :disabled="submitting" class="min-w-0">
      <div
        class="flex flex-col items-start justify-between gap-2 sm:flex-row sm:items-center"
        :class="compact ? 'xl:flex-col xl:items-stretch' : ''"
      >
        <p class="text-base font-bold text-ink">创建你的短链接</p>
        <span class="text-xs text-muted">{{
          isAuthenticated ? '自动保存到我的链接' : '无需注册，即刻分享'
        }}</span>
      </div>
      <div class="mt-4 flex flex-col gap-3 sm:flex-row" :class="compact ? 'xl:flex-col' : ''">
        <Input
          v-model="targetURL"
          class="min-w-0 flex-1"
          type="url"
          placeholder="粘贴长链接，例如 https://example.com"
          autocomplete="off"
          :error="fieldErrors.target_url"
          aria-label="长链接"
        />
        <Button type="submit" :loading="submitting" :disabled="!canSubmit" class="sm:shrink-0">{{
          submitting ? '生成中' : '生成短链'
        }}</Button>
      </div>
      <div class="mt-4 flex flex-wrap items-center justify-between gap-3">
        <div class="flex min-w-0 flex-wrap gap-2 text-xs">
          <span class="rounded-sm border border-ink bg-surface-card px-2 py-1 break-anywhere">{{
            expirySummary
          }}</span>
          <span v-if="passwordEnabled" class="rounded-sm border border-ink bg-surface-cream-strong px-2 py-1"
            >口令保护</span
          >
        </div>
        <button
          type="button"
          class="link-quiet inline-flex items-center gap-2 text-sm font-bold text-ink"
          :aria-expanded="advancedOpen"
          :aria-controls="advancedId"
          @click="advancedOpen = !advancedOpen"
        >
          {{ advancedOpen ? '收起高级选项' : '高级选项' }}
          <span aria-hidden="true">{{ advancedOpen ? '−' : '+' }}</span>
        </button>
      </div>
      <p class="mt-2 text-xs leading-relaxed text-muted">有效期从生成时开始计算，到期后将停止跳转。</p>

      <div v-show="advancedOpen" :id="advancedId" class="mt-5 space-y-5 border-t-2 border-ink pt-5">
        <section class="rounded-sm border-2 border-ink bg-surface-soft p-4">
          <h3 class="text-sm font-bold text-ink">01 / 有效期</h3>
          <div class="mt-3">
            <Select v-model="expiryChoice" :options="expiryOptions" label="多久后过期" />
            <Input
              v-if="expiryChoice === 'custom'"
              v-model="expiresAt"
              class="mt-3"
              label="到期时间"
              type="datetime-local"
              :error="fieldErrors.expires_at"
              hint="按你当前设备的本地时间设置"
            />
            <p v-if="expiryChoice === 'permanent'" class="mt-3 text-xs text-muted">
              不会自动过期，你可以随时在详情页停用或删除。
            </p>
          </div>
        </section>
        <section class="rounded-sm border-2 border-ink p-4">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <h3 class="text-sm font-bold text-ink">02 / 访问保护</h3>
            <label class="flex cursor-pointer items-center gap-2 text-sm text-ink"
              ><input
                v-model="passwordEnabled"
                type="checkbox"
                class="h-4 w-4 accent-primary"
              />启用访问口令</label
            >
          </div>
          <p class="mt-2 text-xs leading-relaxed text-muted">
            {{
              passwordEnabled
                ? '分享时，请将访问口令一并告知对方。'
                : '当前为公开访问，拿到短链的人都可以打开。'
            }}
          </p>
          <div v-if="passwordEnabled" class="mt-3">
            <Input
              v-model="password"
              label="访问口令"
              :type="passwordVisible ? 'text' : 'password'"
              autocomplete="new-password"
              placeholder="至少 8 位"
              :error="fieldErrors.password"
              hint="与管理密钥不同，仅用于打开短链"
            />
            <button
              type="button"
              class="text-link mt-2 text-xs"
              :aria-pressed="passwordVisible"
              @click="passwordVisible = !passwordVisible"
            >
              {{ passwordVisible ? '隐藏口令' : '显示口令' }}
            </button>
          </div>
        </section>
        <section class="rounded-sm border-2 border-ink p-4">
          <h3 class="text-sm font-bold text-ink">03 / 命名与整理</h3>
          <div class="mt-3 space-y-4">
            <Input
              v-model="customCode"
              label="自定义短码（可选）"
              placeholder="例如 hello-miku"
              :maxlength="32"
              :error="fieldErrors.custom_code"
              hint="留空自动生成；3–32 位字母、数字、- 或 _"
            />
            <Input
              v-model="title"
              label="标题（可选）"
              placeholder="给这条链接起个名字"
              :maxlength="200"
              :error="fieldErrors.title"
            />
            <Input
              v-model="tags"
              label="标签（可选）"
              placeholder="例如 工作, 资料"
              :error="fieldErrors.tags"
              hint="用逗号分隔，最多 10 个，每个不超过 32 个字符"
            />
            <div v-if="parsedTags.length" class="flex flex-wrap gap-2" aria-label="标签预览">
              <span v-for="(tag, index) in parsedTags" :key="index" class="badge badge-quiet break-anywhere"
                >#{{ tag }}</span
              >
            </div>
          </div>
        </section>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <button type="button" class="link-quiet text-xs text-muted" @click="resetOptions">
            恢复默认设置
          </button>
          <Button type="submit" :loading="submitting" :disabled="!canSubmit">按当前设置生成</Button>
        </div>
      </div>
      <p v-if="formError" role="alert" class="mt-4 text-sm text-error">{{ formError }}</p>
    </fieldset>
  </form>
</template>
