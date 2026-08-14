<script setup>
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { authApi } from '../api'
import { auth } from '../store/auth'

const route = useRoute()
const router = useRouter()
const form = ref({ email: '', password: '' })
const error = ref('')
const loading = ref(false)

async function submit() {
  error.value = ''
  if (!form.value.email || !form.value.password) {
    error.value = '请填写邮箱和密码'
    return
  }
  loading.value = true
  try {
    const data = await authApi.login(form.value)
    auth.setSession(data.token, data.user)
    router.push(route.query.redirect || '/')
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="page page-narrow">
    <div class="auth-card">
      <h1>登录</h1>
      <p class="sub">继续你的阅读与批注</p>
      <form @submit.prevent="submit">
        <div class="field">
          <label for="email">邮箱</label>
          <input id="email" v-model.trim="form.email" class="input" type="email" placeholder="you@example.com" />
        </div>
        <div class="field">
          <label for="password">密码</label>
          <input id="password" v-model="form.password" class="input" type="password" placeholder="至少 6 位" />
        </div>
        <p v-if="error" class="error-text">{{ error }}</p>
        <button class="btn btn-primary btn-block" type="submit" :disabled="loading">
          {{ loading ? '登录中…' : '登录' }}
        </button>
      </form>
      <p class="auth-switch">
        还没有账号？<RouterLink to="/register">注册一个</RouterLink>
      </p>
    </div>
  </div>
</template>
