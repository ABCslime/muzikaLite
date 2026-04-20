<template>
  <Teleport to="body">
    <Transition name="modal">
      <div
        v-if="show"
        class="fixed inset-0 z-50 flex items-center justify-center bg-black bg-opacity-70"
        @click.self="$emit('close')"
      >
        <div
          class="bg-white pixel-card pixel-texture-light border-vibrant-purple p-6 max-w-md w-full mx-4"
          @click.stop
        >
          <div class="flex justify-between items-center mb-4">
            <h2 class="text-2xl font-bold text-gray-900">{{ title }}</h2>
            <button
              @click="$emit('close')"
              class="text-gray-600 hover:text-vibrant-pink transition-colors"
            >
              <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M6 18L18 6M6 6l12 12"
                />
              </svg>
            </button>
          </div>
          <div class="text-gray-700">
            <slot />
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup>
defineProps({
  show: {
    type: Boolean,
    required: true,
  },
  title: {
    type: String,
    default: '',
  },
})

defineEmits(['close'])
</script>

<style scoped>
.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.3s ease;
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}
</style>

