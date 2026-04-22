<template>
  <!-- v0.4.2 PR A: a pinned genre shown on Home as a toggle-off button.
       "Active" state (colored background) = currently pinned. Click to
       unpin — the chip removes itself from the list as the preferences
       API confirms. Source is stamped into the background color so
       users can tell Bandcamp tags (pink) from Discogs genres (sky).

       v0.6.1: when similar mode is active AND this chip is a Discogs
       genre, the chip renders in a "filter active" visual state
       (muted background, tooltip explains the filter role). Bandcamp
       tags aren't affected — they don't participate in the filter
       because Discogs candidates can't be reliably matched against
       bandcamp's free-form vocabulary. -->
  <button
    type="button"
    :class="[
      'px-3 py-1 pixel-border text-xs font-semibold transition-colors',
      chipClass,
    ]"
    :title="tooltip"
    @click="$emit('unpin')"
  >
    <span>{{ label }}</span>
    <span class="ml-2 opacity-70">×</span>
  </button>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  label: { type: String, required: true },
  source: { type: String, required: true, validator: v => ['bandcamp', 'discogs'].includes(v) },
  // v0.6.1: parent Home view passes the current similar-mode
  // status. When active AND this chip is a Discogs genre, we
  // render the "filter role" visual + tooltip instead of the
  // plain "pinned as refill seed" one.
  similarActive: { type: Boolean, default: false },
})
defineEmits(['unpin'])

const filterRole = computed(() =>
  props.similarActive && props.source === 'discogs',
)

const chipClass = computed(() => {
  if (filterRole.value) {
    // Muted but still readable. Purple tint matches the similar-
    // mode pin's color family so the user reads "genre chip is
    // working WITH similar mode" rather than "greyed out = dead."
    return 'bg-vibrant-purple-light/60 border-vibrant-purple/60 text-gray-700 hover:bg-red-200 hover:border-red-500 hover:text-gray-900'
  }
  return props.source === 'discogs'
    ? 'bg-vibrant-sky-light border-vibrant-sky text-gray-900 hover:bg-red-200 hover:border-red-500'
    : 'bg-vibrant-pink-light border-vibrant-pink text-white hover:bg-red-200 hover:border-red-500 hover:text-gray-900'
})

const tooltip = computed(() => {
  if (filterRole.value) {
    return `Filtering similar-mode picks to ${props.label}. Click to unpin.`
  }
  if (props.similarActive && props.source === 'bandcamp') {
    return `${props.label} is ignored while similar mode is active (Bandcamp tags don't participate in the filter). Click to unpin.`
  }
  return `Click to unpin ${props.label}`
})
</script>
