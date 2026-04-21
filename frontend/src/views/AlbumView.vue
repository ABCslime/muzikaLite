<template>
  <div class="flex h-screen">
    <Sidebar />
    <div class="flex-1 flex flex-col overflow-hidden">
      <TopBar />
      <div class="flex-1 overflow-y-auto pb-24">
        <div v-if="loading" class="flex items-center justify-center h-full">
          <p class="text-gray-600">Loading album…</p>
        </div>
        <div v-else-if="error" class="p-8">
          <div class="bg-red-100 pixel-border border-red-500 p-4 text-red-700 pixel-texture">
            {{ error }}
          </div>
        </div>
        <template v-else>
          <!-- Hero — mirrors PlaylistDetailView composition. Color
               band shifts by availability state so the user sees
               at-a-glance whether this album is fetchable. -->
          <div
            class="px-8 pt-16 pb-8 bg-gradient-to-b"
            :class="heroBgClass"
          >
            <div class="flex items-end space-x-6">
              <div
                class="w-48 h-48 pixel-border flex items-center justify-center flex-shrink-0 shadow-2xl"
                :class="placeholderBgClass"
              >
                <svg class="w-24 h-24 text-white opacity-80" fill="currentColor" viewBox="0 0 20 20">
                  <path d="M18 3a1 1 0 00-1.196-.98l-10 2A1 1 0 006 5v9.114A4.369 4.369 0 005 14c-1.657 0-3 .895-3 2s1.343 2 3 2 3-.895 3-2V7.82l8-1.6v5.894A4.37 4.37 0 0015 12c-1.657 0-3 .895-3 2s1.343 2 3 2 3-.895 3-2V3z" />
                </svg>
              </div>
              <div class="flex-1 min-w-0">
                <p class="text-sm font-semibold text-gray-900 uppercase mb-2">Album</p>
                <h1 class="text-5xl font-bold text-gray-900 mb-4 truncate">{{ detail.title }}</h1>
                <p class="text-gray-800 font-semibold mb-1">{{ detail.artist }}</p>
                <p class="text-gray-700 text-sm">
                  <span v-if="detail.year">{{ detail.year }}</span>
                  <span v-if="detail.label"> · {{ detail.label }}</span>
                  <span v-if="detail.catalogNumber"> · {{ detail.catalogNumber }}</span>
                  <span v-if="detail.tracks.length"> · {{ detail.tracks.length }} tracks</span>
                </p>
                <p
                  v-if="availability === 'checking'"
                  class="text-blue-800 text-xs italic mt-3 flex items-center space-x-1"
                >
                  <svg class="w-3 h-3 animate-spin" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                      d="M4 12a8 8 0 018-8v4a4 4 0 00-4 4H4z" />
                  </svg>
                  <span>Checking Soulseek availability…</span>
                </p>
                <p v-else-if="availability === 'available'" class="text-green-800 text-xs italic mt-3">
                  Available on Soulseek · {{ peerCount }} peer<span v-if="peerCount !== 1">s</span>
                </p>
                <p v-else-if="availability === 'not_found'" class="text-amber-800 text-xs italic mt-3">
                  Not found on Soulseek — Queue will still try the full ladder.
                </p>
              </div>
            </div>
          </div>

          <!-- Action row. -->
          <div class="px-8 py-6 bg-vibrant-bg">
            <div class="flex items-center space-x-4">
              <button
                type="button"
                :disabled="queuing"
                class="w-14 h-14 bg-vibrant-pink pixel-border border-vibrant-pink flex items-center justify-center hover:scale-110 transition-transform pixel-texture-vibrant disabled:opacity-60 disabled:hover:scale-100"
                @click="handleQueueAlbum"
                :title="queuing ? 'Queued' : 'Add album to queue'"
              >
                <svg v-if="!queuing" class="w-6 h-6 text-white ml-0.5" fill="currentColor" viewBox="0 0 20 20">
                  <path fill-rule="evenodd"
                    d="M10 18a8 8 0 100-16 8 8 0 000 16zM9.555 7.168A1 1 0 008 8v4a1 1 0 001.555.832l3-2a1 1 0 000-1.664l-3-2z"
                    clip-rule="evenodd" />
                </svg>
                <svg v-else class="w-6 h-6 text-white" fill="currentColor" viewBox="0 0 20 20">
                  <path fill-rule="evenodd"
                    d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z"
                    clip-rule="evenodd" />
                </svg>
              </button>
              <p class="text-sm font-semibold text-gray-900">
                {{ queuing ? 'Added — check Home' : 'Add album to queue' }}
              </p>
            </div>
          </div>

          <!-- Tracklist. -->
          <div class="px-8 py-4">
            <h2 class="text-xl font-bold text-gray-900 mb-4">Tracklist</h2>
            <div v-if="detail.tracks.length === 0" class="text-gray-600 py-6">
              Discogs has no tracklist for this release.
            </div>
            <ol v-else class="space-y-1">
              <li
                v-for="(t, i) in detail.tracks"
                :key="`${t.position}|${t.title}|${i}`"
                class="flex items-center gap-4 px-4 py-2 bg-pinkish-white pixel-texture"
              >
                <span
                  v-if="t.position"
                  class="text-xs text-gray-500 font-mono min-w-[2.5rem] flex-shrink-0"
                >{{ t.position }}</span>
                <span class="flex-1 text-gray-900 truncate">{{ t.title }}</span>
                <span v-if="t.duration" class="text-xs text-gray-500 font-mono flex-shrink-0">
                  {{ t.duration }}
                </span>
              </li>
            </ol>
          </div>
        </template>
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
import { discogsAPI } from '@/api/discogs'
import { useQueueStore } from '@/stores/queue'

const route = useRoute()
const queueStore = useQueueStore()

const detail = ref({
  id: 0,
  title: '',
  artist: '',
  year: 0,
  catalogNumber: '',
  label: '',
  tracks: [],
})
const loading = ref(true)
const error = ref('')
const queuing = ref(false)

// v0.4.2 PR D: album-level availability. One probe rather than a
// list because Discogs track-level data isn't Soulseek-searchable;
// we just check whether the release exists on Soulseek at all.
// State: 'checking' | 'available' | 'not_found' | 'unknown'.
const availability = ref('unknown')
const peerCount = ref(0)

// Hero gradient tracks availability for at-a-glance feedback.
const heroBgClass = computed(() => {
  switch (availability.value) {
    case 'checking': return 'from-blue-200 to-vibrant-bg'
    case 'available': return 'from-vibrant-pink-light to-vibrant-bg'
    case 'not_found': return 'from-amber-200 to-vibrant-bg'
    default: return 'from-vibrant-purple to-vibrant-bg'
  }
})

const placeholderBgClass = computed(() => {
  switch (availability.value) {
    case 'not_found': return 'bg-gradient-to-br from-amber-400 to-amber-600 border-amber-500'
    default: return 'bg-gradient-to-br from-vibrant-pink to-vibrant-purple border-vibrant-pink'
  }
})

async function load(id) {
  loading.value = true
  error.value = ''
  availability.value = 'unknown'
  peerCount.value = 0
  try {
    detail.value = await discogsAPI.getRelease(id)
    runAvailability()
  } catch (e) {
    const status = e.response?.status
    if (status === 404) error.value = 'Release not found on Discogs.'
    else if (status === 503) error.value = 'Discogs integration is not enabled.'
    else error.value = e.response?.data?.message || e.message || 'Failed to load album.'
  } finally {
    loading.value = false
  }
}

async function runAvailability() {
  if (!detail.value.title || !detail.value.artist) return
  availability.value = 'checking'
  const results = await discogsAPI.checkAvailability([
    {
      title: detail.value.title,
      artist: detail.value.artist,
      catalogNumber: detail.value.catalogNumber || '',
    },
  ])
  const r = results[0]
  if (!r) {
    availability.value = 'unknown'
    return
  }
  peerCount.value = r.peerCount || 0
  availability.value = r.available ? 'available' : 'not_found'
}

async function handleQueueAlbum() {
  if (queuing.value) return
  queuing.value = true
  try {
    const r = await queueStore.searchAndQueue({
      title: detail.value.title,
      artist: detail.value.artist,
      catalogNumber: detail.value.catalogNumber || '',
      query: `${detail.value.artist} — ${detail.value.title}`,
    })
    if (r.success) await queueStore.fetchQueue(true)
  } finally {
    setTimeout(() => { queuing.value = false }, 3000)
  }
}

onMounted(() => load(route.params.id))
watch(() => route.params.id, id => id && load(id))
</script>
