<template>
  <button
    :type="type"
    :disabled="disabled"
    :class="[
      'px-6 py-2 pixel-button font-semibold transition-all duration-200 pixel-texture-vibrant',
      'focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-offset-vibrant-bg',
      disabled
        ? 'bg-gray-300 text-gray-500 cursor-not-allowed border-gray-400'
        : variant === 'primary'
        ? 'bg-vibrant-pink text-white hover:bg-vibrant-pink-light focus:ring-vibrant-pink border-vibrant-pink'
        : variant === 'secondary'
        ? 'bg-white text-gray-900 hover:bg-vibrant-bg focus:ring-vibrant-pink border-gray-900'
        : 'bg-transparent border-vibrant-purple text-gray-900 hover:border-vibrant-pink focus:ring-vibrant-pink',
      size === 'sm' ? 'px-4 py-1.5 text-sm' : size === 'lg' ? 'px-8 py-3 text-lg' : '',
    ]"
    @click="$emit('click', $event)"
  >
    <slot />
  </button>
</template>

<script setup>
defineProps({
  type: {
    type: String,
    default: 'button',
  },
  variant: {
    type: String,
    default: 'primary',
    validator: (value) => ['primary', 'secondary', 'outline'].includes(value),
  },
  size: {
    type: String,
    default: 'md',
    validator: (value) => ['sm', 'md', 'lg'].includes(value),
  },
  disabled: {
    type: Boolean,
    default: false,
  },
})

defineEmits(['click'])
</script>

