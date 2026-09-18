<script setup lang="ts">
/**
 * 创建短链表单 —— 落地页与 Dashboard 共用。
 *
 * 设计意图：默认只露一个输入框（「粘贴长链接」是唯一的心智负担），
 * 自定义短码 / 标题 / 有效期收在「高级选项」里，需要的人再展开。
 */
import { computed, ref } from 'vue'

import { ApiError, linksApi } from '@/api/client'
import type { Link } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import { useAuth } from '@/composables/useAuth'

const emit = defineEmits<{
  (event: 'created', payload: { link: Link; manageKey?: string }): void
}>()

const { rememberManageKey, isAuthenticated } = useAuth()

const targetURL = ref('')
const customCode = ref('')
const title = ref('')
const tags = ref('')
const expiresAt = ref('')
const advancedOpen = ref(false)

const submitting = ref(false)
const formError = ref('')
const fieldErrors = ref<Record<string, string>>({})

const canSubmit = computed(() => targetURL.value.trim().length > 0 && !submitting.value)

/** 把 <input type="datetime-local"> 的本地时间转成 UTC ISO 串。 */
function toISODateTime(local: string): string | undefined {
  if (!local) return undefined
  const date = new Date(local)
  if (Number.isNaN(date.getTime())) return undefined
  return date.toISOString()
}

/** 把逗号分隔的输入拆成标签数组（中文逗号也认）。 */
function splitTags(raw: string): string[] {
  return raw
    .split(/[,，]/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0)
}

function reset(): void {
  targetURL.value = ''
  customCode.value = ''
  title.value = ''
  tags.value = ''
  expiresAt.value = ''
  formError.value = ''
  fieldErrors.value = {}
}

/** 把后端的字段级错误落到对应输入框上，其余归入整体错误。 */
function applyError(error: ApiError): void {
  if (error.isRateLimited) {
    const wait = error.retryAfterSeconds
    formError.value = wait
      ? `操作过于频繁，请 ${wait} 秒后重试`
      : '操作过于频繁，请稍后重试'
    return
  }
  if (error.field) {
    fieldErrors.value = { [error.field]: error.message }
    return
  }
  formError.value = error.friendly
}

async function submit(): Promise<void> {
  if (!canSubmit.value) return

  submitting.value = true
  formError.value = ''
  fieldErrors.value = {}

  try {
    const payload = {
      target_url: targetURL.value.trim(),
      ...(customCode.value.trim() ? { custom_code: customCode.value.trim() } : {}),
      ...(title.value.trim() ? { title: title.value.trim() } : {}),
      ...(splitTags(tags.value).length > 0 ? { tags: splitTags(tags.value) } : {}),
      ...(toISODateTime(expiresAt.value) ? { expires_at: toISODateTime(expiresAt.value) } : {}),
    }
    const result = await linksApi.create(payload)

    // 匿名创建会返回一次性 manage_key：立刻存起来，否则这条链接再也管不了
    if (result.manage_key) {
      rememberManageKey(result.link.short_code, result.manage_key)
    }
    emit('created', { link: result.link, manageKey: result.manage_key })
    reset()
    advancedOpen.value = false
  } catch (cause) {
    if (cause instanceof ApiError) {
      applyError(cause)
    } else {
      formError.value = '创建失败，请稍后重试'
    }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <form class="w-full" novalidate @submit.prevent="submit">
    <div class="flex flex-col gap-3 sm:flex-row">
      <Input
        v-model="targetURL"
        class="flex-1"
        type="url"
        placeholder="粘贴长链接，例如 https://example.com/very/long/path"
        autocomplete="off"
        :error="fieldErrors.target_url"
        aria-label="长链接"
      />
      <Button type="submit" :loading="submitting" :disabled="!canSubmit" class="sm:shrink-0">
        {{ submitting ? '生成中' : '生成短链' }}
      </Button>
    </div>

    <div class="mt-3 flex items-center gap-3">
      <button
        type="button"
        class="text-[13px] text-muted underline-offset-4 hover:text-ink hover:underline"
        :aria-expanded="advancedOpen"
        @click="advancedOpen = !advancedOpen"
      >
        {{ advancedOpen ? '收起高级选项' : '高级选项：自定义短码 / 标题 / 标签 / 有效期' }}
      </button>
      <span v-if="!isAuthenticated" class="text-[13px] text-muted-soft">匿名创建，无需注册</span>
    </div>

    <div v-if="advancedOpen" class="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
      <Input
        v-model="customCode"
        label="自定义短码"
        placeholder="go-blog"
        :maxlength="32"
        :error="fieldErrors.custom_code"
        hint="3–32 位字母、数字、- 或 _"
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
        placeholder="ops, docs"
        :error="fieldErrors.tags"
        hint="逗号分隔，最多 10 个；统一按小写保存"
      />
      <Input
        v-model="expiresAt"
        label="有效期（可选）"
        type="datetime-local"
        :error="fieldErrors.expires_at"
        hint="留空表示永久有效"
      />
    </div>

    <p v-if="formError" class="mt-3 text-[13px] text-error">{{ formError }}</p>
  </form>
</template>
