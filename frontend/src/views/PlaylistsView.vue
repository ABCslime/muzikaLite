<template>
  <div class="flex h-screen">
    <Sidebar />
    <div class="flex-1 flex flex-col overflow-hidden">
      <TopBar />
      <div class="flex-1 overflow-y-auto pb-24">
    <div class="p-8">
      <div class="flex items-center justify-between mb-8">
        <h1 class="text-3xl font-bold text-gray-900">Your Library</h1>
        <button
          @click="showCreateModal = true"
          class="px-6 py-2 bg-vibrant-pink text-white pixel-button border-vibrant-pink font-semibold hover:bg-vibrant-pink-light transition-colors pixel-texture-vibrant"
        >
          Create Playlist
        </button>
      </div>

      <div v-if="playlistStore.loading" class="text-center py-12">
        <p class="text-gray-600">Loading playlists...</p>
      </div>

      <div v-else-if="playlistStore.error" class="bg-red-100 pixel-border border-red-500 p-4 text-red-700 mb-4 pixel-texture">
        {{ playlistStore.error }}
      </div>

      <div v-else-if="playlistStore.playlists.length === 0" class="text-center py-12">
        <p class="text-gray-600 mb-4">You don't have any playlists yet</p>
        <button
          @click="showCreateModal = true"
          class="px-6 py-2 bg-vibrant-pink text-white pixel-button border-vibrant-pink font-semibold hover:bg-vibrant-pink-light transition-colors pixel-texture-vibrant"
        >
          Create Your First Playlist
        </button>
      </div>

      <PlaylistList v-else :playlists="playlistStore.playlists" />

      <!-- Create Playlist Modal -->
      <Modal :show="showCreateModal" title="Create Playlist" @close="showCreateModal = false">
        <form @submit.prevent="handleCreatePlaylist" class="space-y-4">
          <Input
            v-model="newPlaylist.name"
            label="Playlist Name"
            placeholder="My Awesome Playlist"
            required
            :error="createErrors.name"
          />
          <Input
            v-model="newPlaylist.description"
            label="Description (optional)"
            placeholder="A collection of my favorite tracks"
            :error="createErrors.description"
          />
          <div class="flex space-x-4">
            <Button type="submit" :disabled="playlistStore.loading" class="flex-1">
              Create
            </Button>
            <Button
              type="button"
              variant="outline"
              @click="showCreateModal = false"
              class="flex-1"
            >
              Cancel
            </Button>
          </div>
        </form>
      </Modal>
      </div>
      <PlayerBar />
    </div>
  </div>
</div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { usePlaylistStore } from '@/stores/playlist'
import Sidebar from '@/components/layout/Sidebar.vue'
import TopBar from '@/components/layout/TopBar.vue'
import PlayerBar from '@/components/layout/PlayerBar.vue'
import PlaylistList from '@/components/playlist/PlaylistList.vue'
import Modal from '@/components/common/Modal.vue'
import Input from '@/components/common/Input.vue'
import Button from '@/components/common/Button.vue'

const playlistStore = usePlaylistStore()
const showCreateModal = ref(false)
const newPlaylist = ref({
  name: '',
  description: '',
})
const createErrors = ref({})

onMounted(async () => {
  await playlistStore.fetchPlaylists()
})

const handleCreatePlaylist = async () => {
  createErrors.value = {}
  
  if (!newPlaylist.value.name.trim()) {
    createErrors.value.name = 'Playlist name is required'
    return
  }

  const result = await playlistStore.createPlaylist(
    newPlaylist.value.name,
    newPlaylist.value.description || null
  )

  if (result.success) {
    showCreateModal.value = false
    newPlaylist.value = { name: '', description: '' }
    createErrors.value = {}
  } else {
    // Handle error
    console.error(result.error)
  }
}
</script>

