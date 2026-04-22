<template>
  <!-- v0.5 PR F: pin showing the currently-active similar-mode
       seed on Home, peer to GenreChip in shape but visually
       distinct so users can tell at a glance that their queue
       is being seeded by a SONG (purple + lens icon) vs a
       GENRE (pink/sky rectangles).

       When similar mode is off, the pin doesn't render at all —
       no placeholder, no "set a seed" prompt. The PlayerBar
       lens is the canonical entry point; Home is just a
       status surface.

       Click to unpin, same contract as GenreChip — clears
       similar mode and the chip dismisses itself. -->
  <button
    type="button"
    class="inline-flex items-center gap-2 px-3 py-1 pixel-border text-xs font-semibold transition-colors bg-vibrant-purple-light border-vibrant-purple text-gray-900 hover:bg-red-200 hover:border-red-500"
    :title="tooltip"
    @click="$emit('unpin')"
  >
    <!-- Lens icon, matching the PlayerBar's similar-mode button. -->
    <svg class="w-3 h-3 flex-shrink-0" fill="currentColor" viewBox="0 0 20 20">
      <path
        fill-rule="evenodd"
        d="M8 4a4 4 0 100 8 4 4 0 000-8zM2 8a6 6 0 1110.89 3.476l4.817 4.817a1 1 0 01-1.414 1.414l-4.816-4.816A6 6 0 012 8z"
        clip-rule="evenodd"
      />
    </svg>
    <span class="truncate max-w-xs">Similar: {{ displayLabel }}</span>
    <span class="ml-1 opacity-70">×</span>
  </button>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  title: { type: String, default: '' },
  artist: { type: String, default: '' },
})
defineEmits(['unpin'])

// Prefer "Artist — Title" when both available; fall back gracefully.
// Empty pin (no title, no artist) shouldn't render at all — guarded
// at the parent (Home).
const displayLabel = computed(() => {
  const a = (props.artist || '').trim()
  const t = (props.title || '').trim()
  if (a && t) return `${a} — ${t}`
  if (t) return t
  if (a) return a
  return 'this song'
})

const tooltip = computed(() =>
  `Click to stop filling queue with songs similar to ${displayLabel.value}`,
)
</script>
