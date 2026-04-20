<template>
  <aside class="w-64 bg-vibrant-purple-light pixel-texture-vibrant h-full flex flex-col border-r-3 border-vibrant-purple">
    <div class="p-6">
      <router-link to="/" class="flex items-center space-x-2 mb-8">
        <h1 class="text-2xl font-bold text-gray-900">Digeper</h1>
      </router-link>

      <nav class="space-y-2">
        <router-link
          to="/"
          class="flex items-center space-x-4 px-4 py-3 pixel-border transition-colors"
          :class="[
            $route.name === 'Home'
              ? 'bg-vibrant-pink text-white border-vibrant-pink'
              : 'text-gray-700 hover:text-gray-900 hover:bg-vibrant-bg-hover border-transparent',
          ]"
        >
          <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6"
            />
          </svg>
          <span class="font-semibold">Home</span>
        </router-link>

        <router-link
          to="/playlists"
          class="flex items-center space-x-4 px-4 py-3 pixel-border transition-colors"
          :class="[
            $route.name === 'Playlists' || $route.name === 'PlaylistDetail'
              ? 'bg-vibrant-pink text-white border-vibrant-pink'
              : 'text-gray-700 hover:text-gray-900 hover:bg-vibrant-bg-hover border-transparent',
          ]"
        >
          <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M9 19V6l12-3v13M9 19c0 1.105-1.343 2-3 2s-3-.895-3-2 1.343-2 3-2 3 .895 3 2zm12-3c0 1.105-1.343 2-3 2s-3-.895-3-2 1.343-2 3-2 3 .895 3 2zM9 10l12-3"
            />
          </svg>
          <span class="font-semibold">Your Library</span>
        </router-link>
      </nav>
    </div>

    <div class="mt-auto p-6 border-t-3 border-vibrant-purple pixel-texture-vibrant">
      <div class="flex items-center space-x-3 mb-4">
        <div class="w-10 h-10 bg-vibrant-pink pixel-border border-vibrant-pink flex items-center justify-center">
          <span class="text-white font-bold">{{ userInitial }}</span>
        </div>
        <div class="flex-1 min-w-0">
          <p class="text-gray-900 font-semibold truncate">{{ authStore.username }}</p>
          <p v-if="authStore.user?.email" class="text-gray-600 text-sm truncate">
            {{ authStore.user.email }}
          </p>
        </div>
      </div>
      <button
        @click="handleLogout"
        class="w-full px-4 py-2 bg-vibrant-pink text-white pixel-button border-vibrant-pink hover:bg-vibrant-pink-light transition-colors text-sm font-medium pixel-texture-vibrant"
      >
        Log out
      </button>
    </div>
  </aside>
</template>

<script setup>
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const authStore = useAuthStore()

const userInitial = computed(() => {
  const username = authStore.username
  return username ? username.charAt(0).toUpperCase() : 'U'
})

const handleLogout = () => {
  authStore.logout()
  router.push('/login')
}
</script>

