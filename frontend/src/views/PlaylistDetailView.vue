<template>
  <div class="flex h-screen">
    <Sidebar />
    <div class="flex-1 flex flex-col overflow-hidden">
      <TopBar />
      <div class="flex-1 overflow-y-auto pb-24">
    <div v-if="playlistStore.loading" class="flex items-center justify-center h-full">
      <p class="text-gray-600">Loading playlist...</p>
    </div>

    <div v-else-if="playlistStore.error" class="p-8">
      <div class="bg-red-100 pixel-border border-red-500 p-4 text-red-700 pixel-texture">
        {{ playlistStore.error }}
      </div>
    </div>

    <div v-else-if="playlistStore.currentPlaylist && playlistStore.currentPlaylist.playlist" class="relative">
      <!-- Playlist Header -->
      <div
        class="bg-gradient-to-b from-vibrant-purple to-vibrant-bg px-8 pt-16 pb-8"
      >
        <div class="flex items-end space-x-6">
          <div class="w-48 h-48 bg-gradient-to-br from-vibrant-pink to-vibrant-orange rounded-md flex items-center justify-center flex-shrink-0 shadow-2xl">
            <svg class="w-24 h-24 text-white opacity-80" fill="currentColor" viewBox="0 0 20 20">
              <path
                d="M18 3a1 1 0 00-1.196-.98l-10 2A1 1 0 006 5v9.114A4.369 4.369 0 005 14c-1.657 0-3 .895-3 2s1.343 2 3 2 3-.895 3-2V7.82l8-1.6v5.894A4.37 4.37 0 0015 12c-1.657 0-3 .895-3 2s1.343 2 3 2 3-.895 3-2V3z"
              />
            </svg>
          </div>
          <div class="flex-1 min-w-0">
            <p class="text-sm font-semibold text-gray-900 uppercase mb-2">Playlist</p>
            <h1 class="text-5xl font-bold text-gray-900 mb-4 truncate">
              {{ playlistStore.currentPlaylist.playlist?.name || 'Untitled Playlist' }}
            </h1>
            <p v-if="playlistStore.currentPlaylist.playlist?.description" class="text-gray-700 mb-4">
              {{ playlistStore.currentPlaylist.playlist.description }}
            </p>
            <div class="flex items-center space-x-4">
              <span class="text-gray-700 text-sm">
                {{ playlistStore.currentPlaylist.songs?.length || 0 }} songs
              </span>
            </div>
          </div>
        </div>
      </div>

      <!-- Playlist Actions -->
      <div class="px-8 py-6 bg-vibrant-bg">
        <div class="flex items-center space-x-4">
          <button
            @click="handlePlayAll"
            class="w-14 h-14 bg-vibrant-pink pixel-border border-vibrant-pink flex items-center justify-center hover:scale-110 transition-transform pixel-texture-vibrant"
          >
            <svg class="w-6 h-6 text-white ml-0.5" fill="currentColor" viewBox="0 0 20 20">
              <path
                fill-rule="evenodd"
                d="M10 18a8 8 0 100-16 8 8 0 000 16zM9.555 7.168A1 1 0 008 8v4a1 1 0 001.555.832l3-2a1 1 0 000-1.664l-3-2z"
                clip-rule="evenodd"
              />
            </svg>
          </button>
          <button
            @click="handleDeletePlaylist"
            class="text-gray-600 hover:text-vibrant-pink transition-colors"
            title="Delete playlist"
          >
            <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
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

      <!-- Songs List -->
      <div class="px-8 py-4">
        <div v-if="!playlistStore.currentPlaylist.songs || playlistStore.currentPlaylist.songs.length === 0" class="text-center py-12">
          <p class="text-gray-600 mb-4">This playlist is empty</p>
          <p class="text-gray-500 text-sm">Add songs from the queue to get started</p>
        </div>
        <SongList
          v-else
          :songs="playlistStore.currentPlaylist.songs"
          :song-data="songDataMap"
          :show-remove="true"
          @play="handlePlaySong"
          @remove="handleRemoveSong"
        />
      </div>
      <PlayerBar />
    </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { usePlaylistStore } from '@/stores/playlist'
import { usePlayerStore } from '@/stores/player'
import { useQueueStore } from '@/stores/queue'
import Sidebar from '@/components/layout/Sidebar.vue'
import TopBar from '@/components/layout/TopBar.vue'
import PlayerBar from '@/components/layout/PlayerBar.vue'
import SongList from '@/components/playlist/SongList.vue'

const route = useRoute()
const router = useRouter()
const playlistStore = usePlaylistStore()
const playerStore = usePlayerStore()
const queueStore = useQueueStore()

const songDataMap = ref({})

onMounted(async () => {
  await loadPlaylist()
  await loadSongData()
})

watch(() => route.params.id, async () => {
  await loadPlaylist()
  await loadSongData()
})

const loadPlaylist = async () => {
  const playlistId = route.params.id
  if (playlistId) {
    await playlistStore.fetchPlaylist(playlistId)
  }
}

const loadSongData = async () => {
  // Fetch song details from queue store
  await queueStore.fetchQueue()
  
  // Create a map of song IDs to song data
  const map = {}
  queueStore.songs.forEach(song => {
    map[song.id] = song
  })
  
  // Also check if songs in playlist are in the queue
  if (playlistStore.currentPlaylist?.songs && Array.isArray(playlistStore.currentPlaylist.songs)) {
    playlistStore.currentPlaylist.songs.forEach(playlistSong => {
      const song = queueStore.songs.find(s => s.id === playlistSong.songId)
      if (song) {
        map[playlistSong.songId] = song
      }
    })
  }
  
  songDataMap.value = map
}

const handlePlayAll = () => {
  if (playlistStore.currentPlaylist?.songs && Array.isArray(playlistStore.currentPlaylist.songs)) {
    const songs = playlistStore.currentPlaylist.songs
      .map(ps => songDataMap.value[ps.songId])
      .filter(Boolean)
    
    if (songs.length > 0) {
      playerStore.setQueue(songs, 0)
    }
  }
}

const handlePlaySong = (song) => {
  // Just play the clicked song without adding all playlist songs to queue
  if (song && song.id) {
    playerStore.play(song)
  }
}

const handleRemoveSong = async (playlistSong) => {
  const playlistId = route.params.id
  if (playlistId) {
    const result = await playlistStore.removeSongFromPlaylist(playlistId, playlistSong.songId)
    if (result.success) {
      await loadPlaylist()
    }
  }
}

const handleDeletePlaylist = async () => {
  if (confirm('Are you sure you want to delete this playlist?')) {
    const playlistId = route.params.id
    if (playlistId) {
      const result = await playlistStore.deletePlaylist(playlistId)
      if (result.success) {
        router.push('/playlists')
      }
    }
  }
}
</script>

