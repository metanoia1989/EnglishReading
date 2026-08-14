import { reactive } from 'vue'
import { authApi } from '../api'

const TOKEN_KEY = 'er_token'
const USER_KEY = 'er_user'

export const auth = reactive({
  token: localStorage.getItem(TOKEN_KEY) || '',
  user: JSON.parse(localStorage.getItem(USER_KEY) || 'null'),
  ready: false,

  get isLoggedIn() {
    return Boolean(this.token && this.user)
  },

  async init() {
    if (this.ready) return
    if (!this.token) {
      this.ready = true
      return
    }
    try {
      const data = await authApi.me()
      this.user = data.user
      localStorage.setItem(USER_KEY, JSON.stringify(data.user))
    } catch {
      this.clear()
    } finally {
      this.ready = true
    }
  },

  setSession(token, user) {
    this.token = token
    this.user = user
    localStorage.setItem(TOKEN_KEY, token)
    localStorage.setItem(USER_KEY, JSON.stringify(user))
  },

  clear() {
    this.token = ''
    this.user = null
    localStorage.removeItem(TOKEN_KEY)
    localStorage.removeItem(USER_KEY)
  },
})
