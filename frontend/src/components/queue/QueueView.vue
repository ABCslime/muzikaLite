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
import { ref } from 'vue'
import { onMounted } from 'vue'
import { useQueueStore } from '@/stores/queue'
import { usePlayerStore } from '@/stores/player'
import SongItem from './SongItem.vue'
import PlaylistSelectionModal from '@/components/playlist/PlaylistSelectionModal.vue'

const queueStore = useQueueStore()
const playerStore = usePlayerStore()

const showPlaylistModal = ref(false)
const selectedSong = ref(null)

onMounted(async () => {
  await queueStore.fetchQueue()
})

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

