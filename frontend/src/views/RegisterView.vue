<script setup>
import { nextTick, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { authApi } from '../api'
import { auth } from '../store/auth'

const route = useRoute()
const router = useRouter()
const form = ref({ email: '', password: '', confirm: '', nickname: '' })
const error = ref('')
const loading = ref(false)

const showCode = ref(false)
const devCode = ref('')
const codeInput = ref('')
const codeError = ref('')
const codeInputEl = ref(null)

async function sendCode() {
  error.value = ''
  if (!form.value.email || !form.value.password) {
    error.value = '请填写邮箱和密码'
    return
  }
  if (form.value.password.length < 6) {
    error.value = '密码至少需要 6 位'
    return
  }
  if (form.value.password !== form.value.confirm) {
    error.value = '两次输入的密码不一致'
    return
  }
  loading.value = true
  try {
    const data = await authApi.register({
      email: form.value.email,
      password: form.value.password,
      nickname: form.value.nickname,
    })
    devCode.value = data.devCode || ''
    showCode.value = true
    codeError.value = ''
    await nextTick()
    codeInputEl.value?.focus()
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function verify() {
  codeError.value = ''
  if (!codeInput.value.trim()) {
    codeError.value = '请输入验证码'
    return
  }
  loading.value = true
  try {
    const data = await authApi.verify({ email: form.value.email, code: codeInput.value.trim() })
    auth.setSession(data.token, data.user)
    router.push(route.query.redirect || '/')
  } catch (e) {
    codeError.value = e.message
  } finally {
    loading.value = false
  }
}

function closeCode() {
  showCode.value = false
  codeInput.value = ''
}
</script>

<template>
  <div class="page page-narrow">
    <div class="auth-card">
      <h1>注册</h1>
      <p class="sub">邮箱验证目前为模拟发送，验证码会直接弹出</p>
      <form @submit.prevent="sendCode">
        <div class="field">
          <label for="email">邮箱</label>
          <input id="email" v-model.trim="form.email" class="input" type="email" placeholder="you@example.com" />
        </div>
        <div class="field">
          <label for="nickname">昵称（可选）</label>
          <input id="nickname" v-model.trim="form.nickname" class="input" placeholder="怎么称呼你" />
        </div>
        <div class="field">
          <label for="password">密码</label>
          <input id="password" v-model="form.password" class="input" type="password" placeholder="至少 6 位" />
        </div>
        <div class="field">
          <label for="confirm">确认密码</label>
          <input id="confirm" v-model="form.confirm" class="input" type="password" placeholder="再输入一次" />
        </div>
        <p v-if="error" class="error-text">{{ error }}</p>
        <button class="btn btn-primary btn-block" type="submit" :disabled="loading">
          {{ loading ? '发送中…' : '发送验证码并注册' }}
        </button>
      </form>
      <p class="auth-switch">
        已有账号？<RouterLink to="/login">直接登录</RouterLink>
      </p>
    </div>

    <!-- 模拟邮箱验证码弹窗 -->
    <Transition name="fade">
      <div v-if="showCode" class="modal-mask" @click.self="closeCode">
        <div class="code-modal">
          <div class="mail-icon">✉️</div>
          <h2>验证码已发送（模拟邮件）</h2>
          <p class="tip">
            邮件已发送至 <strong>{{ form.email }}</strong>。开发阶段无需查收邮箱，验证码如下：
          </p>
          <div class="code-display">{{ devCode }}</div>
          <input
            ref="codeInputEl"
            v-model="codeInput"
            class="input code-input"
            placeholder="输入上面的 6 位验证码"
            maxlength="6"
            @keyup.enter="verify"
          />
          <p v-if="codeError" class="error-text">{{ codeError }}</p>
          <div class="modal-actions">
            <button class="btn btn-ghost" @click="closeCode">取消</button>
            <button class="btn btn-primary" :disabled="loading" @click="verify">
              {{ loading ? '验证中…' : '完成注册' }}
            </button>
          </div>
        </div>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.modal-mask {
  position: fixed;
  inset: 0;
  z-index: 100;
  background: rgba(24, 24, 27, 0.45);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
}

.code-modal {
  width: min(420px, 100%);
  background: #fff;
  border-radius: 20px;
  padding: 28px;
  box-shadow: 0 24px 64px rgba(0, 0, 0, 0.22);
  text-align: center;
}

.mail-icon {
  font-size: 38px;
}

.code-modal h2 {
  margin: 10px 0 8px;
  font-size: 20px;
}

.tip {
  color: var(--muted);
  font-size: 13.5px;
  line-height: 1.7;
  margin: 0 0 16px;
}

.code-display {
  font-size: 30px;
  font-weight: 800;
  letter-spacing: 0.45em;
  color: var(--brand);
  background: var(--brand-soft);
  border: 1px dashed #c7d2fe;
  border-radius: 12px;
  padding: 12px 0 12px 8px;
  margin-bottom: 18px;
}

.code-input {
  text-align: center;
  font-size: 16px;
  letter-spacing: 0.3em;
}

.modal-actions {
  display: flex;
  gap: 10px;
  justify-content: flex-end;
  margin-top: 18px;
}

@media (max-width: 640px) {
  .code-modal {
    padding: 22px 16px;
    border-radius: 18px;
  }

  .code-display {
    font-size: 24px;
    letter-spacing: 0.3em;
    padding: 11px 0 11px 7px;
  }

  .code-input {
    font-size: 16px;
  }

  .modal-actions .btn {
    flex: 1;
    min-height: 42px;
  }
}
</style>
