<template>
  <div class="space-y-1">
    <div
      v-for="(playlistSong, index) in songs"
      :key="playlistSong.songId"
      class="flex items-center space-x-4 px-4 py-2 pixel-texture bg-pinkish-white hover:bg-pinkish-white-hover group cursor-pointer transition-colors"
      @click="handlePlay(playlistSong)"
    >
      <div class="w-10 h-10 bg-vibrant-pink-light pixel-border border-vibrant-pink flex items-center justify-center flex-shrink-0 relative overflow-hidden">
        <img
          :src="getSongImageForItem(playlistSong)"
          :alt="getSongTitle(playlistSong)"
          :data-song-id="playlistSong.songId"
          class="w-full h-full object-cover pixelated"
          @error="handleImageError"
        />
        <!-- Play/Pause overlay -->
        <div
          v-if="!isPlaying(playlistSong)"
          class="absolute inset-0 bg-vibrant-pink bg-opacity-40 flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity"
        >
          <span class="text-white text-sm font-semibold">{{ index + 1 }}</span>
        </div>
        <!-- Playing indicator -->
        <div
          v-if="isPlaying(playlistSong)"
          class="absolute inset-0 bg-vibrant-pink bg-opacity-40 flex items-center justify-center"
        >
          <svg
            class="w-5 h-5 text-white"
            fill="currentColor"
            viewBox="0 0 20 20"
          >
            <path
              fill-rule="evenodd"
              d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zM7 8a1 1 0 012 0v4a1 1 0 11-2 0V8zm5-1a1 1 0 00-1 1v4a1 1 0 102 0V8a1 1 0 00-1-1z"
              clip-rule="evenodd"
            />
          </svg>
        </div>
        <div
          v-if="isPlaying(playlistSong)"
          class="absolute inset-0 pixel-border border-vibrant-pink"
        ></div>
      </div>

      <div class="flex-1 min-w-0">
        <p
          class="text-gray-900 font-medium text-sm truncate"
          :class="{ 'text-vibrant-pink': isPlaying(playlistSong) }"
        >
          {{ getSongTitle(playlistSong) }}
        </p>
        <p class="text-gray-600 text-xs truncate">
          {{ getSongArtist(playlistSong) }}
        </p>
      </div>

      <div class="flex items-center space-x-4">
        <button
          @click.stop.prevent="handleAddToQueue(playlistSong)"
          :disabled="isSongInQueue(playlistSong) || isAdding"
          :class="[
            'transition-opacity',
            isSongInQueue(playlistSong) || isAdding
              ? 'opacity-0 group-hover:opacity-40 text-gray-500 cursor-not-allowed pointer-events-none'
              : 'opacity-70 group-hover:opacity-100 text-vibrant-purple hover:text-vibrant-pink cursor-pointer'
          ]"
          :title="isSongInQueue(playlistSong) ? 'Already in queue' : isAdding ? 'Adding...' : 'Add to queue'"
          type="button"
        >
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M4 6h16M4 12h16M4 18h16"
            />
          </svg>
        </button>
        <button
          v-if="showRemove"
          @click.stop="handleRemove(playlistSong)"
          class="text-vibrant-purple hover:text-red-500 opacity-0 group-hover:opacity-100 transition-opacity"
          title="Remove from playlist"
        >
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"
            />
          </svg>
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { usePlayerStore } from '@/stores/player'
import { useQueueStore } from '@/stores/queue'
import { getSongTitle as getTitle, getSongArtist as getArtist, getSongImage } from '@/utils/songHelpers'

const props = defineProps({
  songs: {
    type: Array,
    default: () => [],
  },
  songData: {
    type: Object,
    default: () => ({}),
  },
  showRemove: {
    type: Boolean,
    default: false,
  },
})

const emit = defineEmits(['play', 'remove'])

const playerStore = usePlayerStore()
const queueStore = useQueueStore()

const imageErrors = ref(new Set())
const isAdding = ref(false)
const addingSongId = ref(null)

// Computed property to track which songs are in the queue (for reactivity)
const songsInQueue = computed(() => {
  if (!queueStore || !Array.isArray(queueStore.songs)) {
    return new Set()
  }
  return new Set(queueStore.songs.map(song => song?.id).filter(Boolean))
})

const isPlaying = (playlistSong) => {
  return (
    playerStore.currentSong?.id === playlistSong.songId && playerStore.isPlaying
  )
}

const getSongTitle = (playlistSong) => {
  const song = props.songData[playlistSong.songId]
  return getTitle(song)
}

const getSongArtist = (playlistSong) => {
  const song = props.songData[playlistSong.songId]
  return getArtist(song)
}

const getSongImageForItem = (playlistSong) => {
  const song = props.songData[playlistSong.songId]
  if (imageErrors.value.has(playlistSong.songId)) {
    // Return placeholder if image already failed to load
    return getSongImage(null)
  }
  return getSongImage(song)
}

const handleImageError = (event) => {
  const songId = event.target.dataset.songId
  if (songId) {
    imageErrors.value.add(songId)
    event.target.src = getSongImage(null) // Set to placeholder
  }
}

const handlePlay = (playlistSong) => {
  const song = props.songData[playlistSong.songId]
  if (song) {
    emit('play', song)
  }
}

const isSongInQueue = (playlistSong) => {
  // Button should be ENABLED when song is NOT in queue
  // Button should be DISABLED when song IS in queue
  if (!playlistSong?.songId) {
    return false // No song ID means not in queue
  }
  const songId = String(playlistSong.songId) // Ensure string comparison
  const queueIds = Array.from(songsInQueue.value).map(id => String(id))
  return queueIds.includes(songId)
}

const handleAddToQueue = async (playlistSong) => {
  // Prevent multiple simultaneous adds
  if (isAdding.value) {
    console.log('Already adding a song, please wait')
    return
  }
  
  // Ensure we're only working with the specific song that was clicked
  if (!playlistSong?.songId) {
    console.error('Invalid playlist song:', playlistSong)
    return
  }
  
  // Prevent adding if song is already in queue
  if (isSongInQueue(playlistSong)) {
    console.log('Song already in queue, skipping add')
    return
  }
  
  // Use songId directly from playlist song (don't require full song data)
  const songId = playlistSong.songId
  
  // Try to get full song data if available, but use songId if not
  const song = props.songData[songId]
  const finalSongId = song?.id || songId
  
  // Prevent adding the same song multiple times
  if (addingSongId.value === finalSongId) {
    console.log('This song is already being added')
    return
  }
  
  // Set loading state for this specific song
  isAdding.value = true
  addingSongId.value = finalSongId
  
  // Add only this specific song to the queue (not all songs in playlist)
  try {
    console.log('Adding single song to queue:', finalSongId, song?.title || 'Unknown')
    const result = await queueStore.addSongToQueue(finalSongId, 0)
    if (result.success) {
      console.log('Song added to queue successfully:', finalSongId)
    } else {
      console.error('Failed to add song to queue:', result.error)
    }
  } catch (error) {
    console.error('Error adding song to queue:', error)
  } finally {
    // Reset loading state
    isAdding.value = false
    addingSongId.value = null
  }
}

const handleRemove = (playlistSong) => {
  emit('remove', playlistSong)
}
</script>

