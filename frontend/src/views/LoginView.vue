<script setup lang="ts">
/**
 * 登录页。
 *
 * 表单只有两个字段 —— 这是登录页唯一正确的复杂度。
 * 失败提示统一交给后端的 message（后端刻意不区分「邮箱不存在」与「口令错误」，
 * 前端也不要自作聪明去猜，否则就把账号枚举的口子又开回来了）。
 */
import { computed, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { ApiError } from '@/api/client'
import Button from '@/components/ui/Button.vue'
import AccountLayout from '@/components/AccountLayout.vue'
import Input from '@/components/ui/Input.vue'
import { useAuth } from '@/composables/useAuth'
import { useToast } from '@/composables/useToast'

const route = useRoute()
const router = useRouter()
const toast = useToast()
const { login } = useAuth()

const email = ref('')
const password = ref('')
const submitting = ref(false)
const formError = ref('')

/** 登录成功后要回跳的地址：只接受站内相对路径，避免被构造成开放重定向。 */
const redirectTo = computed(() => {
  const raw = route.query.redirect
  const value = Array.isArray(raw) ? raw[0] : raw
  if (typeof value === 'string' && value.startsWith('/') && !value.startsWith('//')) {
    return value
  }
  return '/dashboard'
})

const canSubmit = computed(() => email.value.trim().length > 0 && password.value.length > 0)

async function submit(): Promise<void> {
  if (!canSubmit.value || submitting.value) return

  submitting.value = true
  formError.value = ''
  try {
    const session = await login({ email: email.value.trim(), password: password.value })
    toast.success(`欢迎回来，${session.user.display_name || session.user.email}`)
    await router.push(redirectTo.value)
  } catch (cause) {
    if (cause instanceof ApiError) {
      formError.value = cause.isRateLimited
        ? `尝试过于频繁，请 ${cause.retryAfterSeconds ?? 600} 秒后重试`
        : cause.friendly
    } else {
      formError.value = '登录失败，请稍后重试'
    }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <AccountLayout mode="login">
    <form class="space-y-5" novalidate @submit.prevent="submit">
      <Input v-model="email" label="邮箱" type="email" placeholder="you@example.com" autocomplete="email" />
      <Input
        v-model="password"
        label="密码"
        type="password"
        placeholder="输入密码"
        autocomplete="current-password"
      />

      <p v-if="formError" role="alert" class="border-2 border-error bg-red-50 p-3 text-[13px] text-error">
        {{ formError }}
      </p>

      <Button type="submit" block :loading="submitting" :disabled="!canSubmit">
        {{ submitting ? '登录中' : '登录' }}
      </Button>
    </form>

    <p class="mt-6 text-center text-[14px] text-muted">
      还没有账号？
      <RouterLink :to="{ name: 'register' }" class="text-link">免费注册</RouterLink>
    </p>

    <p class="mt-5 border-t-2 border-ink/15 pt-5 text-center text-sm text-muted">
      只想试试看？ <RouterLink :to="{ name: 'landing' }" class="text-link">匿名创建短链 ↗</RouterLink>
    </p>
  </AccountLayout>
</template>
