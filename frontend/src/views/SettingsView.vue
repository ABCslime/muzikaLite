<template>
  <!-- v0.4.1 PR C: views self-compose the app shell (Sidebar + TopBar +
       PlayerBar) — matches HomeView/PlaylistsView. Before this, the
       Settings page rendered raw content and hid the whole chrome. -->
  <div class="flex h-screen">
    <Sidebar />
    <div class="flex-1 flex flex-col overflow-hidden">
      <TopBar />
      <div class="flex-1 overflow-y-auto pb-24">
        <div class="p-8 max-w-3xl">
          <h1 class="text-3xl font-bold text-gray-900 mb-2">Settings</h1>
          <p class="text-gray-600 mb-8">
            Pick the genres you want Muzika to seed your queue from. Empty lists
            fall back to the server defaults. Bandcamp and Discogs have separate
            vocabularies — no cross-mapping yet.
          </p>

      <div v-if="prefsStore.loading" class="text-gray-600">Loading…</div>
      <div
        v-else-if="prefsStore.error"
        class="bg-red-100 pixel-border border-red-500 p-4 text-red-700 pixel-texture mb-4"
      >
        {{ prefsStore.error }}
      </div>

      <form v-else @submit.prevent="handleSave" class="space-y-8">
        <!-- Bandcamp tags: free-form, user types comma-separated. -->
        <section>
          <h2 class="text-xl font-bold text-gray-900 mb-2">Bandcamp tags</h2>
          <p class="text-sm text-gray-600 mb-3">
            Free-form tags from Bandcamp's Discover page — e.g.
            <code class="text-vibrant-pink">progressive-house</code>,
            <code class="text-vibrant-pink">vaporwave</code>.
            One per line or comma-separated.
          </p>
          <textarea
            v-model="bandcampRaw"
            rows="4"
            placeholder="progressive-house, vaporwave, minimal-techno"
            class="w-full px-4 py-2 pixel-border border-gray-700 bg-white text-gray-900 focus:outline-none focus:border-vibrant-pink"
          />
          <p class="text-xs text-gray-500 mt-1">
            {{ bandcampCount }} / {{ MAX_ITEMS }} tags
          </p>
        </section>

        <!-- Discogs genres: closed vocabulary, checkboxes. -->
        <section>
          <h2 class="text-xl font-bold text-gray-900 mb-2">Discogs genres</h2>
          <p class="text-sm text-gray-600 mb-3">
            Discogs uses a fixed set of genre names. Pick any number.
          </p>
          <div class="grid grid-cols-2 gap-2">
            <label
              v-for="g in DISCOGS_GENRES"
              :key="g"
              class="flex items-center space-x-2 cursor-pointer"
            >
              <input
                type="checkbox"
                :value="g"
                v-model="selectedDiscogs"
                class="pixel-border border-gray-700"
              />
              <span class="text-gray-900">{{ g }}</span>
            </label>
          </div>
          <p class="text-xs text-gray-500 mt-1">
            {{ selectedDiscogs.length }} / {{ MAX_ITEMS }} genres
          </p>
        </section>

        <div v-if="saveNotice" class="bg-green-100 pixel-border border-green-500 p-3 text-green-800 pixel-texture">
          {{ saveNotice }}
        </div>

        <div class="flex gap-3">
          <button
            type="submit"
            :disabled="saving"
            class="px-6 py-2 bg-vibrant-pink text-white pixel-button border-vibrant-pink font-semibold hover:bg-vibrant-pink-light transition-colors pixel-texture-vibrant disabled:opacity-50"
          >
            {{ saving ? 'Saving…' : 'Save' }}
          </button>
          <button
            type="button"
            @click="handleReset"
            class="px-6 py-2 bg-gray-200 text-gray-900 pixel-button border-gray-500 font-semibold hover:bg-gray-300 transition-colors"
          >
            Reset
          </button>
        </div>
      </form>
        </div>
      </div>
      <PlayerBar />
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { usePreferencesStore } from '@/stores/preferences'
import Sidebar from '@/components/layout/Sidebar.vue'
import TopBar from '@/components/layout/TopBar.vue'
import PlayerBar from '@/components/layout/PlayerBar.vue'

// Discogs' closed genre vocabulary as of 2026. Keep in sync with the
// Discogs API — adding custom genres would fail their search endpoint.
// Source: https://www.discogs.com/developers/#page:database,header:database-search
const DISCOGS_GENRES = [
  'Blues',
  'Brass & Military',
  'Children\'s',
  'Classical',
  'Electronic',
  'Folk, World, & Country',
  'Funk / Soul',
  'Hip Hop',
  'Jazz',
  'Latin',
  'Non-Music',
  'Pop',
  'Reggae',
  'Rock',
  'Stage & Screen',
]

// Must stay in sync with internal/preferences/service.go maxItemsPerSource.
const MAX_ITEMS = 50

const prefsStore = usePreferencesStore()

// Bandcamp tags: edit as a free-form string; parse to list on save.
const bandcampRaw = ref('')
const selectedDiscogs = ref([])
const saving = ref(false)
const saveNotice = ref('')

const bandcampCount = computed(() => parseBandcamp(bandcampRaw.value).length)

function parseBandcamp(raw) {
  if (!raw) return []
  // Split on commas, newlines, semicolons; trim; drop empties; dedupe
  // case-insensitively (matches backend normalizer).
  const seen = new Set()
  const out = []
  for (const part of raw.split(/[,\n;]/)) {
    const trimmed = part.trim()
    if (!trimmed) continue
    const key = trimmed.toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    out.push(trimmed)
  }
  return out
}

async function handleSave() {
  if (saving.value) return
  saving.value = true
  saveNotice.value = ''
  try {
    const tags = parseBandcamp(bandcampRaw.value)
    const result = await prefsStore.save({
      bandcampTags: tags,
      discogsGenres: selectedDiscogs.value,
    })
    if (result.success) {
      // Server normalized the input; reflect that back in the UI.
      bandcampRaw.value = prefsStore.bandcampTags.join(', ')
      selectedDiscogs.value = [...prefsStore.discogsGenres]
      saveNotice.value = 'Preferences saved.'
    }
  } finally {
    saving.value = false
  }
}

function handleReset() {
  bandcampRaw.value = prefsStore.bandcampTags.join(', ')
  selectedDiscogs.value = [...prefsStore.discogsGenres]
  saveNotice.value = ''
}

onMounted(async () => {
  await prefsStore.fetch()
  bandcampRaw.value = prefsStore.bandcampTags.join(', ')
  selectedDiscogs.value = [...prefsStore.discogsGenres]
})
</script>
