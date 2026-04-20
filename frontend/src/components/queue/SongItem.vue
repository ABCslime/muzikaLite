<template>
  <div
    class="flex items-center space-x-4 px-4 py-2 pixel-texture bg-pinkish-white hover:bg-pinkish-white-hover group cursor-pointer transition-colors"
    @click="handlePlay"
  >
    <div class="w-10 h-10 bg-vibrant-pink-light pixel-border border-vibrant-pink flex items-center justify-center flex-shrink-0 relative overflow-hidden">
      <img
        :src="songImage"
        :alt="songTitle"
        class="w-full h-full object-cover pixelated"
        @error="handleImageError"
      />
      <div class="absolute inset-0 bg-vibrant-pink bg-opacity-40 flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity">
        <svg
          v-if="!isPlaying"
          class="w-5 h-5 text-white"
          fill="currentColor"
          viewBox="0 0 20 20"
        >
          <path
            fill-rule="evenodd"
            d="M10 18a8 8 0 100-16 8 8 0 000 16zM9.555 7.168A1 1 0 008 8v4a1 1 0 001.555.832l3-2a1 1 0 000-1.664l-3-2z"
            clip-rule="evenodd"
          />
        </svg>
        <svg
          v-else
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
        v-if="isPlaying"
        class="absolute inset-0 pixel-border border-vibrant-pink"
      ></div>
    </div>

    <div class="flex-1 min-w-0">
      <p
        class="text-gray-900 font-medium text-sm truncate"
        :class="{ 'text-vibrant-pink': isPlaying }"
      >
        {{ songTitle }}
      </p>
      <p class="text-gray-600 text-xs truncate">{{ songArtist }}</p>
    </div>

    <div class="flex items-center space-x-4">
      <p class="text-gray-600 text-sm">{{ formatTime(song.duration) }}</p>
      <button
        @click.stop.prevent="handleAddToQueue"
        :disabled="isInQueue || !props.song?.id"
        :class="[
          'transition-opacity',
          (isInQueue || !props.song?.id)
            ? 'opacity-0 group-hover:opacity-40 text-gray-500 cursor-not-allowed pointer-events-none' 
            : 'opacity-70 group-hover:opacity-100 text-vibrant-purple hover:text-vibrant-pink cursor-pointer'
        ]"
        :title="isInQueue ? 'Already in queue' : !props.song?.id ? 'Invalid song' : 'Add to queue'"
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
        @click.stop="handleAddToPlaylist"
        class="text-vibrant-purple hover:text-vibrant-pink opacity-0 group-hover:opacity-100 transition-opacity"
        title="Add to playlist"
      >
        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M12 4v16m8-8H4"
          />
        </svg>
      </button>
    </div>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { usePlayerStore } from '@/stores/player'
import { useQueueStore } from '@/stores/queue'
import { formatTime } from '@/utils/formatTime'
import { getSongTitle, getSongArtist, getSongImage } from '@/utils/songHelpers'

const props = defineProps({
  song: {
    type: Object,
    required: true,
  },
  index: {
    type: Number,
    default: 0,
  },
})

const emit = defineEmits(['add-to-playlist'])

const playerStore = usePlayerStore()
const queueStore = useQueueStore()

const isPlaying = computed(() => {
  return (
    playerStore.currentSong?.id === props.song.id && playerStore.isPlaying
  )
})

const songTitle = computed(() => getSongTitle(props.song))
const songArtist = computed(() => getSongArtist(props.song))

const songsInQueue = computed(() => {
  if (!queueStore || !Array.isArray(queueStore.songs)) {
    return new Set()
  }
  return new Set(queueStore.songs.map(song => song?.id).filter(Boolean))
})

const isInQueue = computed(() => {
  if (!props.song?.id) {
    return false
  }
  const songId = String(props.song.id)
  const queueIds = Array.from(songsInQueue.value).map(id => String(id))
  return queueIds.includes(songId)
})

const imageError = ref(false)

const songImage = computed(() => {
  if (imageError.value) {
    return getSongImage(null)
  }
  return getSongImage(props.song)
})

const handleImageError = (event) => {
  imageError.value = true
  event.target.src = getSongImage(null)
}

const handlePlay = () => {
  if (isPlaying.value) {
    playerStore.togglePlay()
  } else {
    playerStore.play(props.song)
  }
}

const handleAddToQueue = async () => {
  if (isInQueue.value) {
    console.log('Song already in queue, skipping add')
    return
  }
  
  if (!props.song?.id) {
    console.error('Song does not have an ID:', props.song)
    return
  }
  
  try {
    console.log('Adding song to queue:', props.song.id)
    const result = await queueStore.addSongToQueue(props.song.id, 0)
    if (result.success) {
      console.log('Song added to queue successfully')
    } else {
      console.error('Failed to add song to queue:', result.error)
    }
  } catch (error) {
    console.error('Error adding song to queue:', error)
  }
}

const handleAddToPlaylist = () => {
  emit('add-to-playlist', props.song)
}
</script>

