<template>
  <div class="flex h-screen">
    <Sidebar />
    <div class="flex-1 flex flex-col overflow-hidden">
      <TopBar />
      <div class="flex-1 overflow-y-auto pb-24">
        <div class="p-8 max-w-4xl">
          <div v-if="loading" class="text-gray-600">Loading label…</div>
          <div
            v-else-if="error"
            class="bg-red-100 pixel-border border-red-500 p-4 text-red-700 pixel-texture"
          >
            {{ error }}
          </div>
          <template v-else>
            <h1 class="text-3xl font-bold text-gray-900 mb-1">
              {{ detail.name || `Label #${detail.id}` }}
            </h1>
            <p class="text-sm text-gray-600 mb-6">
              Showing the first {{ detail.releases.length }} release<span v-if="detail.releases.length !== 1">s</span>
              from Discogs. Click
              <strong class="text-vibrant-pink">Queue</strong>
              to acquire via Soulseek.
            </p>
            <ReleaseGrid :releases="detail.releases" />
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
import ReleaseGrid from '@/components/discogs/ReleaseGrid.vue'
import { discogsAPI } from '@/api/discogs'

const route = useRoute()
const detail = ref({ id: 0, name: '', releases: [] })
const loading = ref(true)
const error = ref('')

async function load(id) {
  loading.value = true
  error.value = ''
  try {
    detail.value = await discogsAPI.getLabel(id)
  } catch (e) {
    const status = e.response?.status
    if (status === 404) error.value = 'Label not found on Discogs.'
    else if (status === 503) error.value = 'Discogs integration is not enabled.'
    else error.value = e.response?.data?.message || e.message || 'Failed to load label.'
    detail.value = { id: Number(id), name: '', releases: [] }
  } finally {
    loading.value = false
  }
}

onMounted(() => load(route.params.id))
watch(() => route.params.id, id => id && load(id))
</script>
