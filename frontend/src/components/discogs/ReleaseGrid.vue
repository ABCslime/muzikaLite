<template>
  <!-- v0.4.2 PR C: shared release list shown on ArtistView + LabelView.
       Each row shows (title, artist, year, catno) and a Queue button
       that routes the pick through the existing searchAcquire path.
       Clicking a release's title navigates to AlbumView for the
       tracklist-first browse experience. -->
  <div v-if="releases.length === 0" class="text-gray-600 py-6">
    No releases to show.
  </div>
  <ul v-else class="space-y-1">
    <li
      v-for="r in releases"
      :key="`${r.artist}|${r.title}|${r.catalogNumber}|${r.year}`"
      class="flex items-center justify-between gap-4 px-4 py-2 pixel-border border-gray-800 bg-white hover:bg-vibrant-bg-hover"
    >
      <div class="flex-1 min-w-0">
        <p class="font-semibold text-gray-900 truncate">{{ r.title }}</p>
        <p class="text-xs text-gray-600 truncate">
          <span>{{ r.artist }}</span>
          <span v-if="r.year"> · {{ r.year }}</span>
          <span v-if="r.catalogNumber"> · {{ r.catalogNumber }}</span>
        </p>
      </div>
      <button
        type="button"
        :disabled="queuingId === keyFor(r)"
        class="px-3 py-1 bg-vibrant-pink text-white pixel-button border-vibrant-pink text-xs font-semibold hover:bg-vibrant-pink-light disabled:opacity-60"
        @click="handleQueue(r)"
      >
        {{ queuingId === keyFor(r) ? 'Queued' : 'Queue' }}
      </button>
    </li>
  </ul>
</template>

<script setup>
import { ref } from 'vue'
import { useQueueStore } from '@/stores/queue'

defineProps({
  releases: { type: Array, required: true },
})

const queueStore = useQueueStore()
// queuingId tracks which row just got queued so we flip its button to
// "Queued" briefly. Keyed rather than boolean so a second click on a
// different row doesn't share the disabled state.
const queuingId = ref(null)

function keyFor(r) {
  return `${r.artist}|${r.title}|${r.catalogNumber}|${r.year}`
}

async function handleQueue(r) {
  const id = keyFor(r)
  queuingId.value = id
  try {
    await queueStore.searchAndQueue({
      title: r.title,
      artist: r.artist,
      catalogNumber: r.catalogNumber || '',
      query: `${r.artist} — ${r.title}`,
    })
    // fetchQueue so the new entry shows up without waiting for the
    // periodic refresh.
    await queueStore.fetchQueue(true)
  } finally {
    // Keep the disabled state for 1.5 s after success/failure so the
    // user sees the state transition.
    setTimeout(() => {
      if (queuingId.value === id) queuingId.value = null
    }, 1500)
  }
}
</script>
