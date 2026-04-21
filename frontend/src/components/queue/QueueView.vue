<template>
  <div class="p-6 bg-pinkish-white">
    <div class="flex items-center justify-between mb-6">
      <h2 class="text-2xl font-bold text-gray-900">Queue</h2>
      <button
        v-if="queueStore.songs.length > 0"
        @click="handlePlayAll"
        class="px-6 py-2 bg-vibrant-pink text-white pixel-button border-vibrant-pink font-semibold hover:bg-vibrant-pink-light transition-colors pixel-texture-vibrant"
      >
        Play All
      </button>
    </div>

    <!-- v0.4 PR 3: user-initiated search. Queries the Discogs seeder via
         POST /api/queue/search. A stub is created synchronously; the
         queue entry appears asynchronously as the download ladder
         completes. -->
    <form @submit.prevent="handleSearch" class="mb-6 flex gap-2">
      <input
        v-model="searchQuery"
        type="text"
        placeholder="Search for an artist, release, or track…"
        class="flex-1 px-4 py-2 pixel-border border-gray-700 bg-white text-gray-900 focus:outline-none focus:border-vibrant-pink"
        :disabled="searchBusy"
      />
      <button
        type="submit"
        :disabled="searchBusy || !searchQuery.trim()"
        class="px-6 py-2 bg-vibrant-pink text-white pixel-button border-vibrant-pink font-semibold hover:bg-vibrant-pink-light transition-colors pixel-texture-vibrant disabled:opacity-50"
      >
        {{ searchBusy ? 'Searching…' : 'Search' }}
      </button>
    </form>

    <!-- Search feedback. Transient: cleared on the next fetch that picks
         the stub up, or on a new search. -->
    <div
      v-if="searchNotice"
      class="mb-4 bg-blue-50 pixel-border border-blue-500 p-3 text-sm text-blue-900 pixel-texture"
    >
      {{ searchNotice }}
    </div>
    <div
      v-if="notFoundNotice"
      class="mb-4 bg-amber-50 pixel-border border-amber-500 p-3 text-sm text-amber-900 pixel-texture flex items-center justify-between"
    >
      <span>{{ notFoundNotice }}</span>
      <button
        @click="handleDismissNotFound"
        class="ml-4 px-3 py-1 bg-amber-200 text-amber-900 pixel-border border-amber-700 text-xs font-semibold hover:bg-amber-300"
      >
        Dismiss
      </button>
    </div>
    <div
      v-if="relaxedNotice"
      class="mb-4 bg-amber-50 pixel-border border-amber-500 p-3 text-sm text-amber-900 pixel-texture"
    >
      {{ relaxedNotice }}
    </div>

    <div v-if="queueStore.loading" class="text-center py-12">
      <p class="text-gray-600">Loading queue...</p>
    </div>

    <div v-else-if="queueStore.error" class="bg-red-100 pixel-border border-red-500 p-4 text-red-700 pixel-texture">
      {{ queueStore.error }}
    </div>

    <div v-else-if="queueStore.songs.length === 0" class="text-center py-12">
      <p class="text-gray-600 mb-4">Your queue is empty</p>
      <p class="text-gray-500 text-sm">Add songs to your queue to start playing</p>
    </div>

    <div v-else class="space-y-1">
      <SongItem
        v-for="(song, index) in queueStore.songs"
        :key="song.id"
        :song="song"
        :index="index"
        @add-to-playlist="handleAddToPlaylist"
      />
    </div>

    <PlaylistSelectionModal
      :show="showPlaylistModal"
      :song="selectedSong"
      @close="handleCloseModal"
    />
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { onMounted } from 'vue'
import { useQueueStore } from '@/stores/queue'
import { usePlayerStore } from '@/stores/player'
import SongItem from './SongItem.vue'
import PlaylistSelectionModal from '@/components/playlist/PlaylistSelectionModal.vue'

const queueStore = useQueueStore()
const playerStore = usePlayerStore()

const showPlaylistModal = ref(false)
const selectedSong = ref(null)

// v0.4 PR 3 search state.
const searchQuery = ref('')
const searchBusy = ref(false)

// v0.4.1 PR B — search lifecycle is now visible on the entry itself via
// song.status ('probing' | 'not_found' | 'ready'). We surface notices
// only for the user's most recent search so they correlate the toast
// with what they just typed.

// searchNotice: "Searching Discogs for X…" while the seeder hasn't
// returned metadata yet (entry not in queue OR in queue with no metadata
// — treat absence-of-entry as probing from the user's POV).
const searchNotice = computed(() => {
  const id = queueStore.lastSearchSongId
  if (!id) return null
  const match = queueStore.songs.find(s => s.id === id)
  // Not in queue yet → Discogs still thinking.
  if (!match) return `Searching Discogs for "${queueStore.lastSearchQuery}"…`
  // In queue but probing Soulseek.
  if (match.status === 'probing') {
    return `Found "${match.title || queueStore.lastSearchQuery}" on Discogs — checking Soulseek availability…`
  }
  return null
})

// notFoundNotice (v0.4.1 PR B): the user's search picked something
// Discogs knows but Soulseek doesn't have.
const notFoundNotice = computed(() => {
  const id = queueStore.lastSearchSongId
  if (!id) return null
  const match = queueStore.songs.find(s => s.id === id)
  if (!match || match.status !== 'not_found') return null
  const label = match.artist
    ? `"${match.title}" by ${match.artist}`
    : `"${queueStore.lastSearchQuery}"`
  return `${label} — not available on Soulseek sadly.`
})

// relaxedNotice: ROADMAP §v0.4 item 6 — "no high-quality matches; showing
// best available." Shown when the search-triggered entry arrives with
// relaxed=true.
const relaxedNotice = computed(() => {
  const id = queueStore.lastSearchSongId
  if (!id) return null
  const match = queueStore.songs.find(s => s.id === id)
  if (!match || !match.relaxed || match.status !== 'ready') return null
  return `"${queueStore.lastSearchQuery}" — no high-quality matches; showing best available.`
})

onMounted(async () => {
  await queueStore.fetchQueue()
})

const handleSearch = async () => {
  const q = searchQuery.value.trim()
  if (!q || searchBusy.value) return
  searchBusy.value = true
  try {
    const result = await queueStore.searchAndQueue(q)
    if (result.success) {
      searchQuery.value = ''
      // Kick off one immediate refresh so the user sees the probing entry
      // appear as soon as Discogs picks metadata (typically ~2s). Further
      // polling is handled by the periodic refresh elsewhere; a future
      // v0.5 WebSocket would replace this loop.
      await queueStore.fetchQueue(true)
    }
  } finally {
    searchBusy.value = false
  }
}

// Dismiss the not-found entry the user's last search produced. Removes
// the entry from their queue. The backend does not auto-delete not_found
// entries; the user's explicit action clears the toast.
const handleDismissNotFound = async () => {
  const id = queueStore.lastSearchSongId
  if (!id) return
  await queueStore.removeSongFromQueue(id)
  queueStore.lastSearchSongId = null
  queueStore.lastSearchQuery = null
}

const handlePlayAll = () => {
  if (queueStore.songs.length > 0) {
    // Use merge=false to replace queue when explicitly clicking "Play All"
    playerStore.setQueue(queueStore.songs, 0, false)
  }
}

const handleAddToPlaylist = (song) => {
  selectedSong.value = song
  showPlaylistModal.value = true
}

const handleCloseModal = () => {
  showPlaylistModal.value = false
  selectedSong.value = null
}
</script>
