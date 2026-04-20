<template>
  <header class="h-16 bg-vibrant-sky-light pixel-texture-vibrant flex items-center justify-between px-6 sticky top-0 z-40 border-b-3 border-vibrant-sky">
    <div class="flex-1 max-w-xl">
      <div class="relative">
        <input
          type="text"
          placeholder="What do you want to play?"
          class="w-full px-4 py-2 pl-10 bg-white text-gray-900 pixel-border border-gray-900 pixel-texture-light focus:outline-none focus:ring-2 focus:ring-vibrant-pink"
          v-model="searchQuery"
          @keyup.enter="handleSearch"
        />
        <svg
          class="absolute left-3 top-1/2 transform -translate-y-1/2 w-5 h-5 text-vibrant-purple"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"
          />
        </svg>
      </div>
    </div>
  </header>
</template>

<script setup>
import { ref } from 'vue'

const searchQuery = ref('')

const handleSearch = () => {
  // TODO: Implement search functionality when backend supports it
  console.log('Search:', searchQuery.value)
}
</script>

