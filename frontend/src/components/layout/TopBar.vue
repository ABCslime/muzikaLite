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

      <!-- v0.4.1 PR C: dropdown of Discogs candidates. Shown when:
           - user focused the input AND
           - (we have results OR we're loading OR we have an error to surface)
           User clicks one → queue it → dropdown closes. -->
      <div
        v-if="dropdownOpen"
        class="absolute left-0 right-0 top-full mt-1 bg-white pixel-border border-gray-900 shadow-lg z-50 max-h-96 overflow-y-auto"
      >
        <div v-if="loading" class="px-4 py-3 text-sm text-gray-600">
          Searching Discogs…
        </div>
        <div v-else-if="error" class="px-4 py-3 text-sm text-amber-800 bg-amber-50">
          {{ error }}
        </div>
        <div v-else-if="results.length === 0" class="px-4 py-3 text-sm text-gray-600">
          No matches found.
        </div>
        <ul v-else>
          <li
            v-for="(c, idx) in results"
            :key="`${c.artist}|${c.title}|${c.catalogNumber}|${c.year}`"
            :class="[
              'px-4 py-2 cursor-pointer border-b border-gray-200 last:border-0',
              idx === selectedIdx ? 'bg-vibrant-pink-light' : 'hover:bg-gray-100',
            ]"
            @mousedown.prevent="pick(c)"
            @mouseenter="selectedIdx = idx"
          >
            <div class="font-semibold text-gray-900 truncate">{{ c.title }}</div>
            <div class="text-xs text-gray-600 truncate">
              {{ c.artist }}
              <span v-if="c.year"> · {{ c.year }}</span>
              <span v-if="c.catalogNumber"> · {{ c.catalogNumber }}</span>
            </div>
          </li>
        </ul>
      </div>

      <!-- Post-selection notices, positioned under the input like the dropdown. -->
      <div
        v-if="pendingNotice"
        class="absolute left-0 right-0 top-full mt-1 bg-blue-50 pixel-border border-blue-500 px-3 py-2 text-xs text-blue-900 pixel-texture z-40"
      >
        {{ pendingNotice }}
      </div>
      <!-- v0.4.2 PR A: "not found" is a transient toast now. Backend
           auto-deletes the stub on LoadedStatusNotFound; the toast fires
           when the frontend detects "stub was here, now it's gone" and
           auto-clears after 3 s. No Dismiss button — the queue entry
           is already gone. -->
      <div
        v-else-if="notFoundToast"
        class="absolute left-0 right-0 top-full mt-1 bg-amber-50 pixel-border border-amber-500 px-3 py-2 text-xs text-amber-900 pixel-texture z-40"
      >
        {{ notFoundToast }}
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

// v0.4.1 PR C — TopBar owns the search widget. Two-stage UX:
//   1) User types  -> debounced GET /api/queue/search/preview  -> dropdown
//   2) User clicks -> POST /api/queue/search (pre-picked body) -> stub inserted
// Then queue.onRequestDownload inserts a probing queue_entries row and the
// download worker drives probe -> ladder -> ready | not_found.
const queueStore = useQueueStore()

const searchQuery = ref('')
const results = ref([])
const loading = ref(false)
const error = ref('')
const dropdownOpen = ref(false)
const selectedIdx = ref(-1)

const rootEl = ref(null)

// 250ms debounce balances perceived snappiness with API cost. Discogs
// responses cache for 30 days, so the retry cost for a re-typed prefix
// is near-zero — but we still don't want to fire on every keystroke.
const DEBOUNCE_MS = 250
let debounceTimer = null
let inflightToken = 0

function onInput() {
  if (debounceTimer) clearTimeout(debounceTimer)
  dropdownOpen.value = true
  const q = searchQuery.value.trim()
  if (!q) {
    results.value = []
    loading.value = false
    error.value = ''
    selectedIdx.value = -1
    dropdownOpen.value = false
    return
  }
  debounceTimer = setTimeout(() => runPreview(q), DEBOUNCE_MS)
}

async function runPreview(q) {
  // Token guards against out-of-order responses stomping results.
  const token = ++inflightToken
  loading.value = true
  error.value = ''
  const res = await queueStore.previewSearch(q)
  if (token !== inflightToken) return // a newer query already won
  loading.value = false
  if (res.success) {
    results.value = res.data || []
    selectedIdx.value = res.data?.length ? 0 : -1
    error.value = ''
  } else {
    results.value = []
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

// Click-outside to close. Standard dropdown hygiene.
function onClickAway(e) {
  if (rootEl.value && !rootEl.value.contains(e.target)) {
    dropdownOpen.value = false
  }
}

function moveSel(delta) {
  if (!dropdownOpen.value || results.value.length === 0) return
  const next = selectedIdx.value + delta
  if (next < 0) selectedIdx.value = results.value.length - 1
  else if (next >= results.value.length) selectedIdx.value = 0
  else selectedIdx.value = next
}

function onEnter() {
  if (!dropdownOpen.value) return
  if (selectedIdx.value >= 0 && selectedIdx.value < results.value.length) {
    pick(results.value[selectedIdx.value])
  }
}

async function pick(candidate) {
  dropdownOpen.value = false
  searchQuery.value = ''
  results.value = []
  const result = await queueStore.searchAndQueue({
    title: candidate.title,
    artist: candidate.artist,
    catalogNumber: candidate.catalogNumber || '',
    query: `${candidate.artist} — ${candidate.title}`,
  })
  if (result.success) {
    // Poll once to make the probing entry visible ASAP.
    await queueStore.fetchQueue(true)
  }
}

// v0.4.2 PR A: "searching Discogs…" while we haven't observed the stub
// yet. Once the stub appears (probing), the dropdown collapsed and the
// SongItem itself shows the probing state — no separate notice.
const pendingNotice = computed(() => {
  const id = queueStore.lastSearchSongId
  if (!id) return null
  if (queueStore.lastSearchSawStub) return null
  return `Searching Discogs for "${queueStore.lastSearchQuery}"…`
})

// Transient toast (v0.4.2 PR A). Fires once when the stub disappeared
// from the queue AFTER having been seen — i.e. the backend auto-
// deleted it on NotFound. Clears itself after 3 s.
const notFoundToast = ref('')
let notFoundTimer = null

watch(
  () => [queueStore.lastSearchSongId, queueStore.lastSearchSawStub, queueStore.songs.length],
  () => {
    const id = queueStore.lastSearchSongId
    if (!id || !queueStore.lastSearchSawStub) return
    const stillThere = queueStore.songs.some(s => s.id === id)
    if (stillThere) return
    // Seen, now gone — backend deleted it.
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
})
onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onClickAway)
  if (debounceTimer) clearTimeout(debounceTimer)
  if (notFoundTimer) clearTimeout(notFoundTimer)
})
</script>
