<template>
  <div
    class="bg-white pixel-card pixel-texture-light border-vibrant-purple p-4 hover:bg-vibrant-bg-hover transition-all cursor-pointer group"
    @click="handleClick"
  >
    <div class="w-full aspect-square bg-gradient-to-br from-vibrant-pink to-vibrant-orange pixel-border border-vibrant-pink mb-4 flex items-center justify-center group-hover:scale-105 transition-transform">
      <svg class="w-16 h-16 text-white opacity-80" fill="currentColor" viewBox="0 0 20 20">
        <path
          d="M18 3a1 1 0 00-1.196-.98l-10 2A1 1 0 006 5v9.114A4.369 4.369 0 005 14c-1.657 0-3 .895-3 2s1.343 2 3 2 3-.895 3-2V7.82l8-1.6v5.894A4.37 4.37 0 0015 12c-1.657 0-3 .895-3 2s1.343 2 3 2 3-.895 3-2V3z"
        />
      </svg>
    </div>
    <h3 class="text-gray-900 font-semibold mb-1 truncate">{{ playlist.name }}</h3>
    <p v-if="playlist.description" class="text-gray-600 text-sm truncate">
      {{ playlist.description }}
    </p>
    <p v-else class="text-gray-600 text-sm">Playlist</p>
  </div>
</template>

<script setup>
import { useRouter } from 'vue-router'
import { usePlaylistStore } from '@/stores/playlist'

const props = defineProps({
  playlist: {
    type: Object,
    required: true,
  },
})

const router = useRouter()
const playlistStore = usePlaylistStore()

const handleClick = async () => {
  // Fetch the playlist to load it (but don't add songs to queue)
  const result = await playlistStore.fetchPlaylist(props.playlist.id)
  
  // Only navigate if fetch was successful
  if (result.success && playlistStore.currentPlaylist) {
    router.push(`/playlist/${props.playlist.id}`)
  } else {
    // Error is already set in the store and will be displayed
    console.error('Failed to load playlist:', result.error)
  }
}
</script>

