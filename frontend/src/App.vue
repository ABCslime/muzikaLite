<template>
  <div class="min-h-screen bg-pinkish-white">
    <router-view v-slot="{ Component, route }">
      <Transition name="fade" mode="out-in">
        <component :is="Component" :key="route.path" />
      </Transition>
    </router-view>
  </div>
</template>

<script setup>
import { onMounted } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { useQueueStore } from '@/stores/queue'
import { testServiceConnections } from '@/utils/debug'

const authStore = useAuthStore()
const queueStore = useQueueStore()

onMounted(async () => {
  // Test service connections on app startup
  if (import.meta.env.DEV) {
    console.log('🚀 Running service connection tests...')
    await testServiceConnections()
    
    // Expose test function globally for manual testing
    window.testServices = testServiceConnections
    console.log('💡 Tip: Run testServices() in console to test connections manually')
  }
})
</script>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s ease;
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>

