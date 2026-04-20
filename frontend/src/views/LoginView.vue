<template>
  <div class="min-h-screen bg-pinkish-white flex items-center justify-center px-4">
    <!-- Post-registration loading overlay -->
    <div
      v-if="postRegistrationLoading"
      class="fixed inset-0 bg-pinkish-white bg-opacity-95 flex items-center justify-center z-50"
    >
      <div class="text-center">
        <div class="inline-block relative w-16 h-16 mb-6">
          <div class="absolute inset-0 border-4 border-vibrant-pink border-t-transparent rounded-full animate-spin"></div>
        </div>
        <h2 class="text-2xl font-bold text-gray-900 mb-2">Setting up your account...</h2>
        <p class="text-gray-600">Please wait while we prepare everything for you</p>
      </div>
    </div>

    <div class="w-full max-w-md" :class="{ 'opacity-0 pointer-events-none': postRegistrationLoading }">
      <div class="text-center mb-8">
        <h1 class="text-5xl font-bold text-gray-900 mb-2">Digeper</h1>
        <p class="text-gray-600">A music player that broadens your taste</p>
      </div>

      <div class="bg-white pixel-card pixel-texture-light border-vibrant-purple p-8">
        <div class="flex mb-6 border-b-3 border-gray-300">
          <button
            :class="[
              'flex-1 py-3 font-semibold transition-colors',
              isLogin ? 'text-vibrant-pink border-b-2 border-vibrant-pink' : 'text-gray-600 hover:text-gray-900',
            ]"
            @click="isLogin = true"
          >
            Login
          </button>
          <button
            :class="[
              'flex-1 py-3 font-semibold transition-colors',
              !isLogin ? 'text-vibrant-pink border-b-2 border-vibrant-pink' : 'text-gray-600 hover:text-gray-900',
            ]"
            @click="isLogin = false"
          >
            Register
          </button>
        </div>

        <form @submit.prevent="handleSubmit" class="space-y-4">
          <Input
            v-model="form.username"
            label="Username"
            placeholder="Enter your username"
            required
            :error="errors.username"
          />

          <Input
            v-if="!isLogin"
            v-model="form.email"
            type="email"
            label="Email (optional)"
            placeholder="Enter your email"
            :error="errors.email"
          />

          <Input
            v-model="form.password"
            type="password"
            label="Password"
            placeholder="Enter your password"
            required
            :error="errors.password"
          />

          <div v-if="authStore.error" class="bg-red-900 bg-opacity-50 border border-red-500 rounded p-3 text-red-200 text-sm">
            {{ authStore.error }}
          </div>

          <Button
            type="submit"
            :disabled="authStore.loading || !isFormValid"
            class="w-full"
          >
            <span v-if="authStore.loading">Loading...</span>
            <span v-else>{{ isLogin ? 'Log In' : 'Sign Up' }}</span>
          </Button>
        </form>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import Button from '@/components/common/Button.vue'
import Input from '@/components/common/Input.vue'

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()

const isLogin = ref(true)
const form = ref({
  username: '',
  email: '',
  password: '',
})
const errors = ref({})
const postRegistrationLoading = ref(false)

const isFormValid = computed(() => {
  return form.value.username.trim() && form.value.password.trim() && form.value.password.length >= 6
})

const validateForm = () => {
  errors.value = {}
  
  if (!form.value.username.trim()) {
    errors.value.username = 'Username is required'
  } else if (form.value.username.length < 3) {
    errors.value.username = 'Username must be at least 3 characters'
  }

  if (!form.value.password.trim()) {
    errors.value.password = 'Password is required'
  } else if (form.value.password.length < 6) {
    errors.value.password = 'Password must be at least 6 characters'
  }

  if (!isLogin.value && form.value.email && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(form.value.email)) {
    errors.value.email = 'Invalid email format'
  }

  return Object.keys(errors.value).length === 0
}

const handleSubmit = async () => {
  if (!validateForm()) return

  authStore.clearError()
  
  let result
  const wasRegistration = !isLogin.value
  
  if (isLogin.value) {
    result = await authStore.login(form.value.username, form.value.password)
  } else {
    result = await authStore.register(
      form.value.username,
      form.value.password,
      form.value.email || null
    )
  }

  if (result.success) {
    const redirect = route.query.redirect || '/'
    
    // For registration, show loading animation and wait 4 seconds before redirecting
    if (wasRegistration) {
      postRegistrationLoading.value = true
      setTimeout(() => {
        router.push(redirect)
      }, 4000)
    } else {
      // For login, redirect immediately
      router.push(redirect)
    }
  }
}
</script>

