<script setup>
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { auth } from './store/auth'
import { authApi } from './api'

const route = useRoute()
const router = useRouter()
const bare = computed(() => route.meta.bare)

async function logout() {
  try {
    await authApi.logout()
  } catch {
    // Even if the network call fails we clear the local session.
  }
  auth.clear()
  router.push('/')
}
</script>

<template>
  <div class="app-shell" :class="{ bare }">
    <header v-if="!bare" class="app-nav">
      <RouterLink class="brand" to="/">
        <span class="brand-mark">拾句</span>
        <span class="brand-sub">English Reading</span>
      </RouterLink>

      <nav class="nav-links">
        <RouterLink to="/">书库</RouterLink>
        <template v-if="auth.isLoggedIn">
          <span class="nav-user">👋 {{ auth.user.nickname || auth.user.email }}</span>
          <button class="link-button" @click="logout">退出</button>
        </template>
        <template v-else>
          <RouterLink to="/login">登录</RouterLink>
          <RouterLink class="nav-cta" to="/register">注册</RouterLink>
        </template>
      </nav>
    </header>

    <main class="app-main" :class="{ bare }">
      <RouterView />
    </main>
  </div>
</template>
