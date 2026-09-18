<script setup lang="ts">
/**
 * 注册页。
 *
 * 校验只做「能在前端立刻判断的」两件事（邮箱形态、两次密码一致），
 * 密码强度的最终判定权在后端 —— 前端规则与后端不一致时，以后端为准，
 * 否则会出现「前端说行、后端说不行」的割裂体验。
 */
import { computed, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'

import { ApiError } from '@/api/client'
import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import Input from '@/components/ui/Input.vue'
import { useAuth } from '@/composables/useAuth'
import { useToast } from '@/composables/useToast'

const router = useRouter()
const toast = useToast()
const { register } = useAuth()

const email = ref('')
const password = ref('')
const confirm = ref('')
const displayName = ref('')
const submitting = ref(false)
const formError = ref('')
const fieldErrors = ref<Record<string, string>>({})

const confirmError = computed(() =>
  confirm.value && confirm.value !== password.value ? '两次输入的密码不一致' : '',
)

const canSubmit = computed(
  () =>
    email.value.trim().length > 0 &&
    password.value.length > 0 &&
    confirm.value.length > 0 &&
    !confirmError.value,
)

async function submit(): Promise<void> {
  if (!canSubmit.value || submitting.value) return

  submitting.value = true
  formError.value = ''
  fieldErrors.value = {}

  try {
    const session = await register({
      email: email.value.trim(),
      password: password.value,
      ...(displayName.value.trim() ? { display_name: displayName.value.trim() } : {}),
    })
    toast.success(`注册成功，欢迎 ${session.user.display_name || session.user.email}`)
    await router.push({ name: 'dashboard' })
  } catch (cause) {
    if (cause instanceof ApiError) {
      if (cause.isRateLimited) {
        formError.value = `尝试过于频繁，请 ${cause.retryAfterSeconds ?? 600} 秒后重试`
      } else if (cause.field) {
        fieldErrors.value = { [cause.field]: cause.message }
      } else {
        formError.value = cause.friendly
      }
    } else {
      formError.value = '注册失败，请稍后重试'
    }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="bg-canvas py-16 md:py-24">
    <div class="container-page">
      <div class="mx-auto w-full max-w-md">
        <p class="eyebrow text-center">Get started</p>
        <h1 class="display-lg mt-4 text-center">创建账号</h1>
        <p class="mt-4 text-center text-[15px] text-muted">
          免费，不需要信用卡。注册后立刻可以创建与管理短链。
        </p>

        <Card class="mt-8 p-6 md:p-8">
          <form class="space-y-5" novalidate @submit.prevent="submit">
            <Input
              v-model="email"
              label="邮箱"
              type="email"
              placeholder="you@example.com"
              autocomplete="email"
              :error="fieldErrors.email"
            />
            <Input
              v-model="displayName"
              label="昵称（可选）"
              placeholder="怎么称呼你"
              autocomplete="nickname"
              :maxlength="64"
              :error="fieldErrors.display_name"
            />
            <Input
              v-model="password"
              label="密码"
              type="password"
              placeholder="至少 8 位"
              autocomplete="new-password"
              hint="密码至少 8 位，最多 72 字节"
              :error="fieldErrors.password"
            />
            <Input
              v-model="confirm"
              label="确认密码"
              type="password"
              placeholder="再输入一次"
              autocomplete="new-password"
              :error="confirmError"
            />

            <p v-if="formError" class="text-[13px] text-error">{{ formError }}</p>

            <Button type="submit" block :loading="submitting" :disabled="!canSubmit">
              {{ submitting ? '注册中' : '注册并开始' }}
            </Button>
          </form>

          <p class="mt-6 text-center text-[14px] text-muted">
            已有账号？
            <RouterLink :to="{ name: 'login' }" class="text-link">去登录</RouterLink>
          </p>
        </Card>
      </div>
    </div>
  </div>
</template>
