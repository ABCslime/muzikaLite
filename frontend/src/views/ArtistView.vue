<template>
  <div class="flex h-screen">
    <Sidebar />
    <div class="flex-1 flex flex-col overflow-hidden">
      <TopBar />
      <div class="flex-1 overflow-y-auto pb-24">
        <div v-if="loading" class="flex items-center justify-center h-full">
          <p class="text-gray-600">Loading artist…</p>
        </div>
        <div v-else-if="error" class="p-8">
          <div class="bg-red-100 pixel-border border-red-500 p-4 text-red-700 pixel-texture">
            {{ error }}
          </div>
        </div>
        <template v-else>
          <!-- Hero — mirrors PlaylistDetailView composition. -->
          <div class="bg-gradient-to-b from-vibrant-purple to-vibrant-bg px-8 pt-16 pb-8">
            <div class="flex items-end space-x-6">
              <div
                class="relative w-48 h-48 bg-gradient-to-br from-vibrant-pink to-vibrant-purple pixel-border border-vibrant-pink flex items-center justify-center flex-shrink-0 shadow-2xl overflow-hidden"
              >
                <svg class="w-24 h-24 text-white opacity-80" fill="currentColor" viewBox="0 0 20 20">
                  <path fill-rule="evenodd" d="M10 9a3 3 0 100-6 3 3 0 000 6zm-7 9a7 7 0 1114 0H3z" clip-rule="evenodd" />
                </svg>
                <!-- v0.4.3: show first-release thumbnail as the
                     artist-hero image. Cheaper than a dedicated
                     /artists/{id} Discogs lookup AND more visually
                     relevant (an album cover beats a press photo). -->
                <img
                  v-if="detail.image"
                  :src="detail.image"
                  :alt="detail.name"
                  class="absolute inset-0 w-full h-full object-cover"
                  loading="lazy"
                  referrerpolicy="no-referrer"
                  @error="(e) => e.target.style.display='none'"
                />
              </div>
              <div class="flex-1 min-w-0">
                <p class="text-sm font-semibold text-gray-900 uppercase mb-2">Artist</p>
                <h1 class="text-5xl font-bold text-gray-900 mb-4 truncate">
                  {{ detail.name || 'Artist' }}
                </h1>
                <div class="flex items-center space-x-4 text-gray-700 text-sm">
                  <span>{{ detail.releases.length }} release<span v-if="detail.releases.length !== 1">s</span> on Discogs</span>
                  <span v-if="availabilityChecking">· checking Soulseek availability…</span>
                  <span v-else-if="availableCount !== null">
                    · {{ availableCount }} available on Soulseek
                  </span>
                </div>
              </div>
            </div>
          </div>

          <!-- Action row — parallels the PlaylistDetailView Play-All row.
               Refresh button re-runs the availability probe. -->
          <div class="px-8 py-6 bg-vibrant-bg">
            <div class="flex items-center space-x-4">
              <button
                @click="refreshAvailability"
                :disabled="availabilityChecking || detail.releases.length === 0"
                class="px-4 py-2 bg-white text-gray-900 pixel-border border-gray-700 text-sm font-semibold hover:bg-vibrant-bg-hover disabled:opacity-60"
              >
                {{ availabilityChecking ? 'Checking…' : 'Refresh availability' }}
              </button>
            </div>
          </div>

          <!-- v0.4.4: split. Albums section first (no Soulseek
               probe — the per-track probe runs only when the user
               actually adds the album to a playlist). Then singles,
               which keep the existing per-row Soulseek availability
               check + Queue button. -->
          <div v-if="albums.length" class="px-8 py-4">
            <h2 class="text-xl font-bold text-gray-900 mb-4">
              Albums <span class="text-gray-500 font-normal text-sm">({{ albums.length }})</span>
            </h2>
            <AlbumList
              :releases="albums"
              @add-album="handleAddAlbum"
            />
          </div>

          <div v-if="singles.length" class="px-8 py-4">
            <h2 class="text-xl font-bold text-gray-900 mb-4">
              Singles <span class="text-gray-500 font-normal text-sm">({{ singles.length }})</span>
            </h2>
            <ReleaseGrid
              :releases="singles"
              :availability="availability"
              :availability-checking="availabilityChecking"
            />
          </div>
        </template>

        <PlaylistSelectionModal
          :show="showAlbumModal"
          :album="selectedAlbum"
          @close="showAlbumModal = false"
        />
      </div>
      <PlayerBar />
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import Sidebar from '@/components/layout/Sidebar.vue'
import TopBar from '@/components/layout/TopBar.vue'
import PlayerBar from '@/components/layout/PlayerBar.vue'
import ReleaseGrid from '@/components/discogs/ReleaseGrid.vue'
import AlbumList from '@/components/discogs/AlbumList.vue'
import PlaylistSelectionModal from '@/components/playlist/PlaylistSelectionModal.vue'
import { discogsAPI } from '@/api/discogs'

const route = useRoute()
const detail = ref({ id: 0, name: '', releases: [] })
const loading = ref(true)
const error = ref('')

// v0.4.2 PR D: per-release availability state, parallel to
// detail.releases. Shape: [{available, peerCount}, ...]. Empty until
// the probe completes.
const availability = ref([])
const availabilityChecking = ref(false)

const availableCount = computed(() => {
  if (availability.value.length === 0) return null
  return availability.value.filter(a => a?.available).length
})

// v0.4.4: partition releases into Albums (LP/Album/EP/Mini-Album)
// vs Singles. Backend tags each release with isAlbum. The probe
// only runs over singles — albums defer their per-track probes
// until the user actually adds the album to a playlist.
const albums = computed(() => detail.value.releases.filter(r => r.isAlbum))
const singles = computed(() => detail.value.releases.filter(r => !r.isAlbum))

// "Add full album to playlist" modal state — opened from AlbumList.
const showAlbumModal = ref(false)
const selectedAlbum = ref(null)
function handleAddAlbum(release) {
  selectedAlbum.value = {
    id: release.id,
    title: release.title,
    artist: release.artist,
  }
  showAlbumModal.value = true
}

async function load(id) {
  loading.value = true
  error.value = ''
  availability.value = []
  try {
    detail.value = await discogsAPI.getArtist(id)
    runAvailability()
  } catch (e) {
    const status = e.response?.status
    if (status === 404) error.value = 'Artist not found on Discogs.'
    else if (status === 503) error.value = 'Discogs integration is not enabled.'
    else error.value = e.response?.data?.message || e.message || 'Failed to load artist.'
    detail.value = { id: Number(id), name: '', releases: [] }
  } finally {
    loading.value = false
  }
}

async function runAvailability() {
  // v0.4.4: probe only the singles. Album rows defer their per-track
  // probes until the user adds the album to a playlist (PR C). This
  // keeps the artist-page Soulseek query small AND defensible — even
  // a 50-release discography rarely has more than 5-15 singles.
  const singlesList = singles.value
  if (!singlesList.length) return
  availabilityChecking.value = true
  try {
    if (detail.value.name) {
      availability.value = await discogsAPI.checkAvailabilityByArtist(
        detail.value.name,
        singlesList.map(r => r.title),
      )
    } else {
      availability.value = await discogsAPI.checkAvailability(
        singlesList.map(r => ({
          title: r.title,
          artist: r.artist,
          catalogNumber: r.catalogNumber || '',
        })),
      )
    }
  } finally {
    availabilityChecking.value = false
  }
}

const refreshAvailability = () => runAvailability()

onMounted(() => load(route.params.id))
watch(() => route.params.id, id => id && load(id))
</script>
