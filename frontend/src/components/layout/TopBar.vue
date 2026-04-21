<template>
  <header class="h-16 bg-vibrant-sky-light pixel-texture-vibrant flex items-center justify-between px-6 sticky top-0 z-40 border-b-3 border-vibrant-sky">
    <div class="flex-1 max-w-xl relative" ref="rootEl">
      <div class="relative">
        <input
          type="text"
          placeholder="What do you want to play?"
          class="w-full px-4 py-2 pl-10 bg-white text-gray-900 pixel-border border-gray-900 pixel-texture-light focus:outline-none focus:ring-2 focus:ring-vibrant-pink"
          v-model="searchQuery"
          @input="onInput"
          @focus="onFocus"
          @keydown.down.prevent="moveSel(1)"
          @keydown.up.prevent="moveSel(-1)"
          @keydown.enter.prevent="onEnter"
          @keydown.esc="closeDropdown"
        />
        <svg
          class="absolute left-3 top-1/2 transform -translate-y-1/2 w-5 h-5 text-vibrant-purple"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"
          />
        </svg>
      </div>

      <!-- v0.4.2 PR B: four-section categorized dropdown.
           Genres (green) | Album artists (blue) | Songs (default) | Labels (yellow).
           Different row backgrounds so sections are visually distinct
           at a glance — no need to hunt for section headers in a big
           list. Flat indexing via `flatRows` keeps keyboard nav (↑↓)
           moving through all rows regardless of section. -->
      <div
        v-if="dropdownOpen"
        class="absolute left-0 right-0 top-full mt-1 bg-white pixel-border border-gray-900 shadow-lg z-50 max-h-[32rem] overflow-y-auto"
      >
        <div v-if="loading" class="px-4 py-3 text-sm text-gray-600">
          Searching Discogs…
        </div>
        <div v-else-if="error" class="px-4 py-3 text-sm text-amber-800 bg-amber-50">
          {{ error }}
        </div>
        <div v-else-if="totalResults === 0" class="px-4 py-3 text-sm text-gray-600">
          No matches found.
        </div>
        <template v-else>
          <!-- Genres: pin-to-preferences on click. -->
          <section v-if="results.genres?.length" class="bg-green-50 border-b-2 border-green-600">
            <h3 class="px-3 py-1 text-xs font-bold text-green-900 uppercase tracking-wide">Genres</h3>
            <ul>
              <li
                v-for="(g, i) in results.genres"
                :key="`g-${g}`"
                :class="[
                  'px-4 py-2 cursor-pointer border-b border-green-200 last:border-0 flex items-center justify-between',
                  flatIndex('genres', i) === selectedIdx ? 'bg-green-200' : 'hover:bg-green-100',
                ]"
                @mousedown.prevent="pickGenre(g)"
                @mouseenter="selectedIdx = flatIndex('genres', i)"
              >
                <span class="font-semibold text-gray-900">{{ g }}</span>
                <span
                  class="text-xs font-semibold"
                  :class="isGenrePinned(g) ? 'text-red-700' : 'text-green-700'"
                >
                  {{ isGenrePinned(g) ? 'Unpin −' : 'Pin +' }}
                </span>
              </li>
            </ul>
          </section>

          <!-- Album artists: stub for 4.2.C, non-interactive for now. -->
          <section v-if="results.artists?.length" class="bg-blue-50 border-b-2 border-blue-600">
            <h3 class="px-3 py-1 text-xs font-bold text-blue-900 uppercase tracking-wide">
              Album Artists
            </h3>
            <ul>
              <li
                v-for="(a, i) in results.artists"
                :key="`a-${a.id}`"
                :class="[
                  'px-4 py-2 border-b border-blue-200 last:border-0 flex items-center justify-between',
                  'cursor-not-allowed opacity-70',
                ]"
                :title="'Artist pages land in v0.4.2 PR C'"
              >
                <span class="font-semibold text-gray-900">{{ a.name }}</span>
                <span class="text-xs text-blue-700 italic">coming soon</span>
              </li>
            </ul>
          </section>

          <!-- Songs / Releases: the existing acquire flow. -->
          <section v-if="results.releases?.length" class="bg-white border-b-2 border-gray-400">
            <h3 class="px-3 py-1 text-xs font-bold text-gray-700 uppercase tracking-wide">Songs</h3>
            <ul>
              <li
                v-for="(c, i) in results.releases"
                :key="`r-${c.artist}|${c.title}|${c.catalogNumber}|${c.year}`"
                :class="[
                  'px-4 py-2 cursor-pointer border-b border-gray-200 last:border-0',
                  flatIndex('releases', i) === selectedIdx ? 'bg-vibrant-pink-light' : 'hover:bg-gray-100',
                ]"
                @mousedown.prevent="pickRelease(c)"
                @mouseenter="selectedIdx = flatIndex('releases', i)"
              >
                <div class="font-semibold text-gray-900 truncate">{{ c.title }}</div>
                <div class="text-xs text-gray-600 truncate">
                  {{ c.artist }}
                  <span v-if="c.year"> · {{ c.year }}</span>
                  <span v-if="c.catalogNumber"> · {{ c.catalogNumber }}</span>
                </div>
              </li>
            </ul>
          </section>

          <!-- Labels: stub for 4.2.C. -->
          <section v-if="results.labels?.length" class="bg-yellow-50">
            <h3 class="px-3 py-1 text-xs font-bold text-yellow-900 uppercase tracking-wide">Labels</h3>
            <ul>
              <li
                v-for="(l, i) in results.labels"
                :key="`l-${l.id}`"
                :class="[
                  'px-4 py-2 border-b border-yellow-200 last:border-0 flex items-center justify-between',
                  'cursor-not-allowed opacity-70',
                ]"
                :title="'Label pages land in v0.4.2 PR C'"
              >
                <span class="font-semibold text-gray-900">{{ l.name }}</span>
                <span class="text-xs text-yellow-700 italic">coming soon</span>
              </li>
            </ul>
          </section>
        </template>
      </div>

      <!-- Post-selection notices. Pending + transient not-found toast +
           relaxed + new in 4.2.B: "pinned" confirmation toast. -->
      <div
        v-if="pendingNotice"
        class="absolute left-0 right-0 top-full mt-1 bg-blue-50 pixel-border border-blue-500 px-3 py-2 text-xs text-blue-900 pixel-texture z-40"
      >
        {{ pendingNotice }}
      </div>
      <div
        v-else-if="notFoundToast"
        class="absolute left-0 right-0 top-full mt-1 bg-amber-50 pixel-border border-amber-500 px-3 py-2 text-xs text-amber-900 pixel-texture z-40"
      >
        {{ notFoundToast }}
      </div>
      <div
        v-else-if="pinToast"
        class="absolute left-0 right-0 top-full mt-1 bg-green-50 pixel-border border-green-500 px-3 py-2 text-xs text-green-900 pixel-texture z-40"
      >
        {{ pinToast }}
      </div>
      <div
        v-else-if="relaxedNotice"
        class="absolute left-0 right-0 top-full mt-1 bg-amber-50 pixel-border border-amber-500 px-3 py-2 text-xs text-amber-900 pixel-texture z-40"
      >
        {{ relaxedNotice }}
      </div>
    </div>
  </header>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useQueueStore } from '@/stores/queue'
import { usePreferencesStore } from '@/stores/preferences'

const queueStore = useQueueStore()
const prefsStore = usePreferencesStore()

const searchQuery = ref('')
// v0.4.2 PR B: results is now a Preview object with 4 arrays rather
// than a flat candidate list.
const emptyPreview = () => ({ genres: [], artists: [], releases: [], labels: [] })
const results = ref(emptyPreview())
const loading = ref(false)
const error = ref('')
const dropdownOpen = ref(false)
const selectedIdx = ref(-1)

const rootEl = ref(null)

const DEBOUNCE_MS = 250
let debounceTimer = null
let inflightToken = 0

// Total visible rows across all sections — for "No matches found" check.
const totalResults = computed(
  () =>
    (results.value.genres?.length || 0) +
    (results.value.artists?.length || 0) +
    (results.value.releases?.length || 0) +
    (results.value.labels?.length || 0),
)

// Flatten the 4 sections into a single index space for keyboard nav.
// Order matches the visual section order: genres → artists → releases → labels.
// Artists + labels are disabled (stubs for 4.2.C) so they're skipped.
const flatRows = computed(() => {
  const out = []
  for (const g of results.value.genres || []) out.push({ kind: 'genre', payload: g })
  // Artists + labels skipped — 4.2.C wires them as clickable.
  for (const c of results.value.releases || []) out.push({ kind: 'release', payload: c })
  return out
})

// Map a (section, local i) to the flat index used for keyboard nav.
function flatIndex(section, i) {
  let base = 0
  if (section === 'genres') return base + i
  base += results.value.genres?.length || 0
  // Artists don't participate in keyboard nav.
  if (section === 'artists') return -1
  if (section === 'releases') return base + i
  base += results.value.releases?.length || 0
  // Labels don't either.
  if (section === 'labels') return -1
  return -1
}

function onInput() {
  if (debounceTimer) clearTimeout(debounceTimer)
  dropdownOpen.value = true
  const q = searchQuery.value.trim()
  if (!q) {
    results.value = emptyPreview()
    loading.value = false
    error.value = ''
    selectedIdx.value = -1
    dropdownOpen.value = false
    return
  }
  debounceTimer = setTimeout(() => runPreview(q), DEBOUNCE_MS)
}

async function runPreview(q) {
  const token = ++inflightToken
  loading.value = true
  error.value = ''
  const res = await queueStore.previewSearch(q)
  if (token !== inflightToken) return
  loading.value = false
  if (res.success) {
    results.value = res.data || emptyPreview()
    selectedIdx.value = flatRows.value.length ? 0 : -1
    error.value = ''
  } else {
    results.value = emptyPreview()
    selectedIdx.value = -1
    if (res.status === 503) {
      error.value = 'Search unavailable — Discogs integration is not enabled on the server.'
    } else if (res.status === 502) {
      error.value = 'Discogs is temporarily unreachable. Try again in a moment.'
    } else {
      error.value = res.error || 'Preview failed.'
    }
  }
}

function onFocus() {
  if (searchQuery.value.trim()) dropdownOpen.value = true
}

function closeDropdown() {
  dropdownOpen.value = false
}

function onClickAway(e) {
  if (rootEl.value && !rootEl.value.contains(e.target)) {
    dropdownOpen.value = false
  }
}

function moveSel(delta) {
  if (!dropdownOpen.value || flatRows.value.length === 0) return
  const n = flatRows.value.length
  const next = (selectedIdx.value + delta + n) % n
  selectedIdx.value = next
}

function onEnter() {
  if (!dropdownOpen.value) return
  if (selectedIdx.value < 0 || selectedIdx.value >= flatRows.value.length) return
  const row = flatRows.value[selectedIdx.value]
  if (row.kind === 'genre') pickGenre(row.payload)
  else if (row.kind === 'release') pickRelease(row.payload)
}

// v0.4.2 PR B — genre click: toggle pin on Discogs preferences. A
// short success toast replaces the dropdown briefly; the Home chip
// appears (or disappears) reactively via prefsStore state.
const pinToast = ref('')
let pinTimer = null

async function pickGenre(g) {
  dropdownOpen.value = false
  // Fetch once if we haven't yet — usually Home or Settings has
  // already done this, but TopBar lives on every page.
  if (!prefsStore.bandcampTags && !prefsStore.discogsGenres) {
    await prefsStore.fetch()
  }
  const wasPinned = isGenrePinned(g)
  const res = await prefsStore.toggleDiscogsGenre(g)
  if (res.success) {
    pinToast.value = wasPinned
      ? `Unpinned "${g}" from your genres.`
      : `Pinned "${g}" — refiller will seed from it.`
    if (pinTimer) clearTimeout(pinTimer)
    pinTimer = setTimeout(() => {
      pinToast.value = ''
      pinTimer = null
    }, 2500)
  } else {
    error.value = res.error || 'Failed to update preferences.'
    dropdownOpen.value = true
  }
}

function isGenrePinned(g) {
  return (prefsStore.discogsGenres || []).some(x => x.toLowerCase() === g.toLowerCase())
}

async function pickRelease(candidate) {
  dropdownOpen.value = false
  searchQuery.value = ''
  results.value = emptyPreview()
  const result = await queueStore.searchAndQueue({
    title: candidate.title,
    artist: candidate.artist,
    catalogNumber: candidate.catalogNumber || '',
    query: `${candidate.artist} — ${candidate.title}`,
  })
  if (result.success) {
    await queueStore.fetchQueue(true)
  }
}

// Notices (pending / not-found / relaxed) unchanged from PR A.2.
const pendingNotice = computed(() => {
  const id = queueStore.lastSearchSongId
  if (!id) return null
  if (queueStore.lastSearchSawStub) return null
  return `Searching Discogs for "${queueStore.lastSearchQuery}"…`
})

const notFoundToast = ref('')
let notFoundTimer = null

watch(
  () => [queueStore.lastSearchSongId, queueStore.lastSearchSawStub, queueStore.songs.length],
  () => {
    const id = queueStore.lastSearchSongId
    if (!id || !queueStore.lastSearchSawStub) return
    const stillThere = queueStore.songs.some(s => s.id === id)
    if (stillThere) return
    const label = queueStore.lastSearchQuery
      ? `"${queueStore.lastSearchQuery}"`
      : 'That'
    notFoundToast.value = `${label} — not available on Soulseek sadly.`
    queueStore.lastSearchSongId = null
    queueStore.lastSearchQuery = null
    queueStore.lastSearchSawStub = false
    if (notFoundTimer) clearTimeout(notFoundTimer)
    notFoundTimer = setTimeout(() => {
      notFoundToast.value = ''
      notFoundTimer = null
    }, 3000)
  },
)

const relaxedNotice = computed(() => {
  const id = queueStore.lastSearchSongId
  if (!id) return null
  const match = queueStore.songs.find(s => s.id === id)
  if (!match || !match.relaxed || match.status !== 'ready') return null
  return `"${queueStore.lastSearchQuery}" — no high-quality matches; showing best available.`
})

onMounted(() => {
  document.addEventListener('mousedown', onClickAway)
  // Make sure prefsStore is loaded so pin/unpin labels in the dropdown
  // reflect current state from the moment it opens.
  if (!prefsStore.discogsGenres?.length && !prefsStore.loading) {
    prefsStore.fetch()
  }
})
onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onClickAway)
  if (debounceTimer) clearTimeout(debounceTimer)
  if (notFoundTimer) clearTimeout(notFoundTimer)
  if (pinTimer) clearTimeout(pinTimer)
})
</script>
