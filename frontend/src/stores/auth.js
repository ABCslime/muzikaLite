import { defineStore } from 'pinia'
import { authAPI } from '@/api/auth'
import { STORAGE_KEYS } from '@/utils/constants'

export const useAuthStore = defineStore('auth', {
  state: () => ({
    user: JSON.parse(localStorage.getItem(STORAGE_KEYS.USER)) || null,
    token: localStorage.getItem(STORAGE_KEYS.TOKEN) || null,
    loading: false,
    error: null,
  }),

  getters: {
    isAuthenticated: (state) => !!state.token,
    username: (state) => state.user?.username || '',
    userId: (state) => state.user?.userId || state.user?.id || null,
  },

  actions: {
    async login(username, password) {
      this.loading = true
      this.error = null
      try {
        const response = await authAPI.login(username, password)
        this.token = response.token
        this.user = {
          userId: response.userId,
          username: response.username,
          email: response.email,
        }
        localStorage.setItem(STORAGE_KEYS.TOKEN, response.token)
        localStorage.setItem(STORAGE_KEYS.USER, JSON.stringify(this.user))
        return { success: true }
      } catch (error) {
        this.error = error.response?.data?.message || 'Login failed'
        return { success: false, error: this.error }
      } finally {
        this.loading = false
      }
    },

    async register(username, password, email = null) {
      this.loading = true
      this.error = null
      try {
        await authAPI.register(username, password, email)
        // Auto-login after registration
        return await this.login(username, password)
      } catch (error) {
        this.error = error.response?.data?.message || 'Registration failed'
        return { success: false, error: this.error }
      } finally {
        this.loading = false
      }
    },

    logout() {
      this.token = null
      this.user = null
      this.error = null
      localStorage.removeItem(STORAGE_KEYS.TOKEN)
      localStorage.removeItem(STORAGE_KEYS.USER)
    },

    clearError() {
      this.error = null
    },
  },
})

