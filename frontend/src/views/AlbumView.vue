<template>
  <div class="flex h-screen">
    <Sidebar />
    <div class="flex-1 flex flex-col overflow-hidden">
      <TopBar />
      <div class="flex-1 overflow-y-auto pb-24">
        <div class="p-8 max-w-3xl">
          <div v-if="loading" class="text-gray-600">Loading album…</div>
          <div
            v-else-if="error"
            class="bg-red-100 pixel-border border-red-500 p-4 text-red-700 pixel-texture"
          >
            {{ error }}
          </div>
          <template v-else>
            <div class="mb-6">
              <h1 class="text-3xl font-bold text-gray-900 mb-1">{{ detail.title }}</h1>
              <p class="text-sm text-gray-700">
                <span class="font-semibold">{{ detail.artist }}</span>
                <span v-if="detail.year"> · {{ detail.year }}</span>
                <span v-if="detail.label"> · {{ detail.label }}</span>
                <span v-if="detail.catalogNumber"> · {{ detail.catalogNumber }}</span>
              </p>
            </div>

            <div class="flex items-center gap-3 mb-8">
              <button
                type="button"
                :disabled="queuing"
                class="px-6 py-2 bg-vibrant-pink text-white pixel-button border-vibrant-pink font-semibold hover:bg-vibrant-pink-light disabled:opacity-60 pixel-texture-vibrant"
                @click="handleQueueAlbum"
              >
                {{ queuing ? 'Queued' : 'Add album to queue' }}
              </button>
              <p v-if="queueNotice" class="text-xs text-gray-600">{{ queueNotice }}</p>
            </div>

            <h2 class="text-lg font-bold text-gray-900 mb-2">Tracklist</h2>
            <div v-if="detail.tracks.length === 0" class="text-gray-600">
              Discogs has no tracklist for this release.
            </div>
            <ol v-else class="space-y-1">
              <li
                v-for="(t, i) in detail.tracks"
                :key="`${t.position}|${t.title}|${i}`"
                class="flex items-center gap-3 px-3 py-2 pixel-border border-gray-700 bg-white"
              >
                <span
                  v-if="t.position"
                  class="text-xs text-gray-500 font-mono min-w-[2.5rem]"
                >{{ t.position }}</span>
                <span class="flex-1 text-gray-900 truncate">{{ t.title }}</span>
                <span v-if="t.duration" class="text-xs text-gray-500 font-mono">{{ t.duration }}</span>
              </li>
            </ol>
          </template>
        </div>
      </div>
      <PlayerBar />
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, watch } from 'vue'
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
const queueNotice = ref('')

async function load(id) {
  loading.value = true
  error.value = ''
  try {
    detail.value = await discogsAPI.getRelease(id)
  } catch (e) {
    const status = e.response?.status
    if (status === 404) error.value = 'Release not found on Discogs.'
    else if (status === 503) error.value = 'Discogs integration is not enabled.'
    else error.value = e.response?.data?.message || e.message || 'Failed to load album.'
  } finally {
    loading.value = false
  }
}

// Add-album-to-queue routes through searchAndQueue with (title, artist,
// catalogNumber) — same path as the search dropdown. The backend's
// catalog-dedup (PR A.1) kicks in if the user already has this exact
// album, so clicking here on an already-queued album is safe.
async function handleQueueAlbum() {
  if (queuing.value) return
  queuing.value = true
  queueNotice.value = ''
  try {
    const r = await queueStore.searchAndQueue({
      title: detail.value.title,
      artist: detail.value.artist,
      catalogNumber: detail.value.catalogNumber || '',
      query: `${detail.value.artist} — ${detail.value.title}`,
    })
    if (r.success) {
      queueNotice.value = 'Added to queue — check the Home view.'
      await queueStore.fetchQueue(true)
    } else {
      queueNotice.value = r.error || 'Queue add failed.'
    }
  } finally {
    setTimeout(() => {
      queuing.value = false
      queueNotice.value = ''
    }, 3000)
  }
}

onMounted(() => load(route.params.id))
watch(() => route.params.id, id => id && load(id))
</script>
