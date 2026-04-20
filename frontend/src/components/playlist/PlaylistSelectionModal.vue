<template>
  <Modal :show="show" title="Add to Playlist" @close="handleClose">
    <div v-if="addingSongId" class="text-center py-4">
      <p class="text-gray-600">Adding song to playlist...</p>
    </div>

    <div v-else-if="successMessage" class="text-center py-4">
      <p class="text-vibrant-pink mb-4">{{ successMessage }}</p>
      <button
        @click="handleClose"
        class="px-4 py-2 bg-vibrant-pink text-white pixel-button border-vibrant-pink font-semibold hover:bg-vibrant-pink-light transition-colors pixel-texture-vibrant"
      >
        Close
      </button>
    </div>

    <div v-else-if="playlistStore.loading" class="text-center py-8">
      <p class="text-gray-600">Loading playlists...</p>
    </div>

    <div v-else-if="playlistStore.error" class="bg-red-100 pixel-border border-red-500 p-4 text-red-700 mb-4 pixel-texture">
      {{ playlistStore.error }}
      <button
        @click="fetchPlaylists"
        class="mt-2 text-red-700 hover:text-red-900 underline"
      >
        Try again
      </button>
    </div>

    <div v-else-if="playlistStore.playlists.length === 0" class="text-center py-8">
      <p class="text-gray-600 mb-4">No playlists found</p>
      <p class="text-gray-500 text-sm">Create a playlist first to add songs to it</p>
    </div>

    <div v-else class="max-h-96 overflow-y-auto">
      <div class="space-y-2">
        <button
          v-for="playlist in playlistStore.playlists"
          :key="playlist.id"
          @click="handleSelectPlaylist(playlist.id)"
          class="w-full text-left px-4 py-3 pixel-border border-transparent hover:border-vibrant-pink hover:bg-vibrant-bg-hover transition-colors group pixel-texture"
        >
          <div class="flex items-center space-x-3">
            <div class="w-12 h-12 bg-gradient-to-br from-vibrant-pink to-vibrant-orange pixel-border border-vibrant-pink flex items-center justify-center flex-shrink-0">
              <svg class="w-6 h-6 text-white opacity-80" fill="currentColor" viewBox="0 0 20 20">
                <path
                  d="M18 3a1 1 0 00-1.196-.98l-10 2A1 1 0 006 5v9.114A4.369 4.369 0 005 14c-1.657 0-3 .895-3 2s1.343 2 3 2 3-.895 3-2V7.82l8-1.6v5.894A4.37 4.37 0 0015 12c-1.657 0-3 .895-3 2s1.343 2 3 2 3-.895 3-2V3z"
                />
              </svg>
            </div>
            <div class="flex-1 min-w-0">
              <p class="text-gray-900 font-medium truncate">{{ playlist.name }}</p>
              <p v-if="playlist.description" class="text-gray-600 text-sm truncate">
                {{ playlist.description }}
              </p>
              <p v-else class="text-gray-600 text-sm">Playlist</p>
            </div>
            <svg
              class="w-5 h-5 text-vibrant-purple opacity-0 group-hover:opacity-100 transition-opacity"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M9 5l7 7-7 7"
              />
            </svg>
          </div>
        </button>
      </div>
    </div>
  </Modal>
</template>

<script setup>
import { ref, watch } from 'vue'
import { usePlaylistStore } from '@/stores/playlist'
import Modal from '@/components/common/Modal.vue'
import { getSongTitle } from '@/utils/songHelpers'

const props = defineProps({
  show: {
    type: Boolean,
    required: true,
  },
  song: {
    type: Object,
    default: null,
  },
})

const emit = defineEmits(['close'])

const playlistStore = usePlaylistStore()
const addingSongId = ref(null)
const successMessage = ref('')

watch(() => props.show, async (isOpen) => {
  if (isOpen) {
    successMessage.value = ''
    addingSongId.value = null
    await fetchPlaylists()
  }
})

const fetchPlaylists = async () => {
  await playlistStore.fetchPlaylists()
}

const handleSelectPlaylist = async (playlistId) => {
  if (!props.song?.id) {
    return
  }

  addingSongId.value = playlistId
  const result = await playlistStore.addSongToPlaylist(playlistId, props.song.id)
  
  if (result.success) {
    const playlist = playlistStore.playlists.find(p => p.id === playlistId)
    const songTitle = getSongTitle(props.song)
    successMessage.value = `"${songTitle}" added to "${playlist?.name || 'playlist'}"`
    
    // Auto-close after 2 seconds
    setTimeout(() => {
      handleClose()
    }, 2000)
  } else {
    addingSongId.value = null
    // Error is already set in the store, so it will be displayed
  }
}

const handleClose = () => {
  successMessage.value = ''
  addingSongId.value = null
  playlistStore.clearError()
  emit('close')
}
</script>

