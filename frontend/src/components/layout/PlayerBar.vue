<template>
  <div class="fixed bottom-0 left-64 right-0 h-24 bg-pinkish-white pixel-texture border-t-3 border-vibrant-pink z-50">
    <div class="flex items-center h-full px-4">
      <!-- Current Song Info -->
      <div class="flex items-center space-x-4 w-1/4 min-w-[180px]">
        <div
          v-if="playerStore.currentSong"
          class="w-14 h-14 bg-vibrant-pink-light pixel-border border-vibrant-pink flex items-center justify-center flex-shrink-0 overflow-hidden"
        >
          <img
            :src="currentSongImage"
            :alt="currentSongTitle"
            class="w-full h-full object-cover pixelated"
            @error="handleImageError"
          />
        </div>
        <div v-if="playerStore.currentSong" class="min-w-0 flex-1">
          <p class="text-gray-900 font-medium text-sm truncate">
            {{ currentSongTitle }}
          </p>
          <p class="text-gray-600 text-xs truncate">
            {{ currentSongArtist }}
          </p>
        </div>
      </div>

      <!-- Playback Controls -->
      <div class="flex-1 flex flex-col items-center justify-center space-y-2">
        <div class="flex items-center space-x-4">
          <button
            @click="playerStore.toggleShuffle"
            class="text-gray-600 hover:text-vibrant-pink transition-colors"
            :class="{ 'text-vibrant-pink': playerStore.shuffle }"
          >
            <svg class="w-5 h-5" fill="currentColor" viewBox="0 0 20 20">
              <path
                d="M10.894 2.553a1 1 0 00-1.788 0l-7 14a1 1 0 001.169 1.409l5-1.429A1 1 0 009 15.571V11a1 1 0 112 0v4.571a1 1 0 00.725.962l5 1.428a1 1 0 001.17-1.408l-7-14z"
              />
            </svg>
          </button>

          <button
            @click="playerStore.previous"
            :disabled="!playerStore.hasPrevious && !playerStore.repeat"
            class="text-gray-900 hover:text-vibrant-pink transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          >
            <svg class="w-6 h-6" fill="currentColor" viewBox="0 0 20 20">
              <path
                d="M8.445 14.832A1 1 0 0010 14v-2.798l5.445 3.63A1 1 0 0017 14V6a1 1 0 00-1.555-.832L10 8.798V6a1 1 0 00-1.555-.832l-6 4a1 1 0 000 1.664l6 4z"
              />
            </svg>
          </button>

          <button
            @click="playerStore.togglePlay"
            class="w-10 h-10 bg-vibrant-pink text-white pixel-border border-vibrant-pink flex items-center justify-center hover:scale-110 transition-transform pixel-texture-vibrant"
            :disabled="!playerStore.currentSong"
          >
            <svg v-if="!playerStore.isPlaying" class="w-5 h-5 ml-0.5" fill="currentColor" viewBox="0 0 20 20">
              <path
                fill-rule="evenodd"
                d="M10 18a8 8 0 100-16 8 8 0 000 16zM9.555 7.168A1 1 0 008 8v4a1 1 0 001.555.832l3-2a1 1 0 000-1.664l-3-2z"
                clip-rule="evenodd"
              />
            </svg>
            <svg v-else class="w-5 h-5" fill="currentColor" viewBox="0 0 20 20">
              <path
                fill-rule="evenodd"
                d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zM7 8a1 1 0 012 0v4a1 1 0 11-2 0V8zm5-1a1 1 0 00-1 1v4a1 1 0 102 0V8a1 1 0 00-1-1z"
                clip-rule="evenodd"
              />
            </svg>
          </button>

          <button
            @click="playerStore.next"
            :disabled="!playerStore.hasNext && !playerStore.repeat"
            class="text-gray-900 hover:text-vibrant-pink transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          >
            <svg class="w-6 h-6" fill="currentColor" viewBox="0 0 20 20">
              <path
                d="M11.555 5.168A1 1 0 0010 6v2.798L4.555 5.168A1 1 0 003 6v8a1 1 0 001.555.832L10 11.202V14a1 1 0 001.555.832l6-4a1 1 0 000-1.664l-6-4z"
              />
            </svg>
          </button>

          <button
            @click="playerStore.toggleRepeat"
            class="text-gray-600 hover:text-vibrant-pink transition-colors"
            :class="{
              'text-vibrant-pink': playerStore.repeat === 'all',
              'text-vibrant-pink': playerStore.repeat === 'one',
            }"
          >
            <svg v-if="playerStore.repeat === 'one'" class="w-5 h-5" fill="currentColor" viewBox="0 0 20 20">
              <path
                fill-rule="evenodd"
                d="M4 2a1 1 0 011 1v2.101a7.002 7.002 0 0111.601 2.566 1 1 0 11-1.885.666A5.002 5.002 0 005.999 7H9a1 1 0 010 2H4a1 1 0 01-1-1V3a1 1 0 011-1zm.008 9.057a1 1 0 011.276.61A5.002 5.002 0 0014.001 13H11a1 1 0 110-2h5a1 1 0 011 1v5a1 1 0 11-2 0v-2.101a7.002 7.002 0 01-11.601-2.566 1 1 0 01.61-1.276z"
                clip-rule="evenodd"
              />
              <circle cx="10" cy="10" r="1" fill="currentColor" />
            </svg>
            <svg v-else class="w-5 h-5" fill="currentColor" viewBox="0 0 20 20">
              <path
                fill-rule="evenodd"
                d="M4 2a1 1 0 011 1v2.101a7.002 7.002 0 0111.601 2.566 1 1 0 11-1.885.666A5.002 5.002 0 005.999 7H9a1 1 0 010 2H4a1 1 0 01-1-1V3a1 1 0 011-1zm.008 9.057a1 1 0 011.276.61A5.002 5.002 0 0014.001 13H11a1 1 0 110-2h5a1 1 0 011 1v5a1 1 0 11-2 0v-2.101a7.002 7.002 0 01-11.601-2.566 1 1 0 01.61-1.276z"
                clip-rule="evenodd"
              />
            </svg>
          </button>

          <button
            @click="playerStore.toggleLiked"
            class="text-gray-400 hover:text-white transition-colors"
            :class="{
              'text-red-500 hover:text-red-400': playerStore.isLiked,
              'text-gray-400 hover:text-white': !playerStore.isLiked,
            }"
            title="Like song"
          >
            <svg
              v-if="playerStore.isLiked"
              class="w-5 h-5"
              fill="currentColor"
              viewBox="0 0 20 20"
            >
              <path
                fill-rule="evenodd"
                d="M3.172 5.172a4 4 0 015.656 0L10 6.343l1.172-1.171a4 4 0 115.656 5.656L10 17.657l-6.828-6.829a4 4 0 010-5.656z"
                clip-rule="evenodd"
              />
            </svg>
            <svg
              v-else
              class="w-5 h-5"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M4.318 6.318a4.5 4.5 0 000 6.364L12 20.364l7.682-7.682a4.5 4.5 0 00-6.364-6.364L12 7.636l-1.318-1.318a4.5 4.5 0 00-6.364 0z"
              />
            </svg>
          </button>
        </div>

        <!-- Progress Bar -->
        <div class="flex items-center space-x-2 w-full max-w-2xl">
          <span class="text-xs text-gray-600 w-12 text-right">
            {{ formatTime(playerStore.currentTime) }}
          </span>
          <div
            class="flex-1 h-1 bg-gray-300 rounded-full cursor-pointer group"
            @click="handleSeek"
            ref="progressBarRef"
          >
            <div
              class="h-full bg-vibrant-pink rounded-full transition-all group-hover:bg-vibrant-pink-light relative"
              :style="{ width: `${playerStore.progress}%` }"
            >
              <div class="w-3 h-3 bg-vibrant-pink rounded-full opacity-0 group-hover:opacity-100 absolute right-0 top-1/2 transform -translate-y-1/2 translate-x-1/2"></div>
            </div>
          </div>
          <span class="text-xs text-gray-600 w-12">
            {{ formatTime(playerStore.duration) }}
          </span>
        </div>
      </div>

      <!-- Volume Control -->
      <div class="flex items-center space-x-2 w-1/4 justify-end">
        <svg class="w-5 h-5 text-gray-600" fill="currentColor" viewBox="0 0 20 20">
          <path
            fill-rule="evenodd"
            d="M9.383 3.076A1 1 0 0110 4v12a1 1 0 01-1.617.793L4.383 13H2a1 1 0 01-1-1V8a1 1 0 011-1h2.383l4-3.793a1 1 0 011.617.793zM14.657 2.929a1 1 0 011.414 0A9.972 9.972 0 0119 12a9.972 9.972 0 01-2.929 7.071 1 1 0 01-1.414-1.414A7.971 7.971 0 0017 12c0-2.21-.894-4.208-2.343-5.657a1 1 0 010-1.414zm-2.829 2.828a1 1 0 011.415 0A5.983 5.983 0 0115 12a5.984 5.984 0 01-1.757 4.243 1 1 0 01-1.415-1.415A3.984 3.984 0 0013 12a3.983 3.983 0 00-1.172-2.828 1 1 0 010-1.415z"
            clip-rule="evenodd"
          />
        </svg>
        <input
          type="range"
          min="0"
          max="1"
          step="0.01"
          :value="playerStore.volume"
          @input="playerStore.setVolume($event.target.value)"
          class="w-24 h-1 bg-gray-300 rounded-lg appearance-none cursor-pointer accent-vibrant-pink"
        />
      </div>
    </div>

    <!-- Hidden Audio Element -->
    <audio
      ref="audioElement"
      preload="metadata"
      @loadedmetadata="playerStore.setAudioElement($event.target)"
    />
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { usePlayerStore } from '@/stores/player'
import { formatTime } from '@/utils/formatTime'
import { getSongTitle, getSongArtist, getSongImage } from '@/utils/songHelpers'

const playerStore = usePlayerStore()
const audioElement = ref(null)
const progressBarRef = ref(null)
const imageError = ref(false)

// Reset image error when song changes
watch(() => playerStore.currentSong?.id, () => {
  imageError.value = false
})

const currentSongTitle = computed(() => {
  return getSongTitle(playerStore.currentSong)
})

const currentSongArtist = computed(() => {
  return getSongArtist(playerStore.currentSong)
})

const currentSongImage = computed(() => {
  if (imageError.value) {
    return getSongImage(null)
  }
  return getSongImage(playerStore.currentSong)
})

const handleImageError = () => {
  imageError.value = true
}

onMounted(() => {
  if (audioElement.value) {
    playerStore.setAudioElement(audioElement.value)
  }
})

onUnmounted(() => {
  // Cleanup: revoke object URL if player store has one
  // (Most cleanup is handled in the store, but this is a safety measure)
  if (playerStore.currentObjectUrl) {
    URL.revokeObjectURL(playerStore.currentObjectUrl)
    playerStore.currentObjectUrl = null
  }
})

// Note: Audio source is now set directly in the player store's play() action
// No need to watch for currentSong changes here

const handleSeek = (event) => {
  if (!progressBarRef.value || !playerStore.duration) return
  
  const rect = progressBarRef.value.getBoundingClientRect()
  const percent = (event.clientX - rect.left) / rect.width
  const newTime = percent * playerStore.duration
  playerStore.seekTo(newTime)
}
</script>

