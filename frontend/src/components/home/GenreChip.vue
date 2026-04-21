<template>
  <!-- v0.4.2 PR A: a pinned genre shown on Home as a toggle-off button.
       "Active" state (colored background) = currently pinned. Click to
       unpin — the chip removes itself from the list as the preferences
       API confirms. Source is stamped into the background color so
       users can tell Bandcamp tags (pink) from Discogs genres (sky). -->
  <button
    type="button"
    :class="[
      'px-3 py-1 pixel-border text-xs font-semibold transition-colors',
      source === 'discogs'
        ? 'bg-vibrant-sky-light border-vibrant-sky text-gray-900 hover:bg-red-200 hover:border-red-500'
        : 'bg-vibrant-pink-light border-vibrant-pink text-white hover:bg-red-200 hover:border-red-500 hover:text-gray-900',
    ]"
    :title="`Click to unpin ${label}`"
    @click="$emit('unpin')"
  >
    <span>{{ label }}</span>
    <span class="ml-2 opacity-70">×</span>
  </button>
</template>

<script setup>
defineProps({
  label: { type: String, required: true },
  source: { type: String, required: true, validator: v => ['bandcamp', 'discogs'].includes(v) },
})
defineEmits(['unpin'])
</script>
