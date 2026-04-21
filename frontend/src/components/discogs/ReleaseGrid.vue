<template>
  <!-- v0.4.2 PR D: matches SongItem composition — 48×48 placeholder on
       the left, title + subtitle in the middle, actions on the right.
       Row background shifts on availability state so the user can scan
       for what's playable without clicking each Queue.
         default        — checking (pre-probe, or probes still in flight)
         pinkish-white  — available (at least one Soulseek peer)
         amber-50       — not found (zero peers)
         blue-50        — user just clicked Queue; briefly held until
                          status flips to 'probing' on the main queue. -->
  <div v-if="releases.length === 0" class="text-gray-600 py-6 text-center">
    No releases to show.
  </div>
  <ul v-else class="space-y-1">
    <li
      v-for="(r, idx) in releases"
      :key="keyFor(r)"
      class="flex items-center gap-4 px-4 py-2 pixel-texture transition-colors"
      :class="rowClass(r)"
    >
      <!-- Art placeholder. Album art arrives in v0.4.3; for now a
           subtle gradient block that matches SongItem's visual weight. -->
      <div
        class="w-12 h-12 flex-shrink-0 pixel-border flex items-center justify-center"
        :class="placeholderClass(r)"
      >
        <svg class="w-6 h-6 opacity-70" fill="currentColor" viewBox="0 0 20 20">
          <path
            d="M18 3a1 1 0 00-1.196-.98l-10 2A1 1 0 006 5v9.114A4.369 4.369 0 005 14c-1.657 0-3 .895-3 2s1.343 2 3 2 3-.895 3-2V7.82l8-1.6v5.894A4.37 4.37 0 0015 12c-1.657 0-3 .895-3 2s1.343 2 3 2 3-.895 3-2V3z"
          />
        </svg>
      </div>

      <div class="flex-1 min-w-0">
        <p class="font-medium text-gray-900 truncate">{{ r.title }}</p>
        <p class="text-xs text-gray-600 truncate">
          <span>{{ r.artist }}</span>
          <span v-if="r.year"> · {{ r.year }}</span>
          <span v-if="r.catalogNumber"> · {{ r.catalogNumber }}</span>
        </p>
        <p
          v-if="availabilityOf(r) === 'checking'"
          class="text-blue-700 text-xs italic flex items-center gap-1 mt-1"
        >
          <svg class="w-3 h-3 animate-spin" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
              d="M4 12a8 8 0 018-8v4a4 4 0 00-4 4H4z" />
          </svg>
          <span>Checking Soulseek…</span>
        </p>
        <p
          v-else-if="availabilityOf(r) === 'not_found'"
          class="text-amber-700 text-xs italic mt-1"
        >
          Not found on Soulseek — Queue will still try harder.
        </p>
        <p
          v-else-if="availabilityOf(r) === 'available'"
          class="text-green-700 text-xs italic mt-1"
        >
          Available · {{ peerCountOf(r) }} peer<span v-if="peerCountOf(r) !== 1">s</span>
        </p>
      </div>

      <button
        type="button"
        :disabled="queuingId === keyFor(r) || availabilityOf(r) === 'checking'"
        class="px-3 py-1 pixel-border text-xs font-semibold transition-colors"
        :class="queueBtnClass(r)"
        @click="handleQueue(r)"
      >
        {{ queuingId === keyFor(r) ? 'Queued' : 'Queue' }}
      </button>
    </li>
  </ul>
</template>

<script setup>
import { ref } from 'vue'
import { useQueueStore } from '@/stores/queue'

const props = defineProps({
  releases: { type: Array, required: true },
  // Parallel array to releases[] — index i describes releases[i]'s
  // Soulseek availability. Shape: [{available:true, peerCount:12}].
  // Empty / undefined until the check completes.
  availability: { type: Array, default: () => [] },
  // While the parent is still running the check.
  availabilityChecking: { type: Boolean, default: false },
})

const queueStore = useQueueStore()
const queuingId = ref(null)

function keyFor(r) {
  return `${r.artist}|${r.title}|${r.catalogNumber}|${r.year}`
}

// availabilityOf returns one of 'checking' | 'available' | 'not_found' |
// 'unknown'. The UI keys off this. 'unknown' ≈ backend lookup failed;
// we render as a plain row without a pill so the user just sees a
// normal Queue button.
function availabilityOf(r) {
  if (props.availabilityChecking && props.availability.length === 0) return 'checking'
  const idx = props.releases.indexOf(r)
  const entry = props.availability[idx]
  if (entry === undefined) return 'unknown'
  if (entry.available) return 'available'
  return 'not_found'
}

function peerCountOf(r) {
  const idx = props.releases.indexOf(r)
  return props.availability[idx]?.peerCount || 0
}

function rowClass(r) {
  switch (availabilityOf(r)) {
    case 'checking': return 'bg-blue-50'
    case 'not_found': return 'bg-amber-50'
    case 'available': return 'bg-pinkish-white hover:bg-pinkish-white-hover'
    default: return 'bg-white hover:bg-vibrant-bg-hover'
  }
}

function placeholderClass(r) {
  switch (availabilityOf(r)) {
    case 'not_found': return 'bg-amber-100 border-amber-500 text-amber-700'
    case 'available': return 'bg-vibrant-pink-light border-vibrant-pink text-white'
    default: return 'bg-vibrant-purple-light border-vibrant-purple text-gray-700'
  }
}

function queueBtnClass(r) {
  // Not-found rows CAN still be queued — the full ladder might rescue
  // what a short probe missed. Visual de-emphasis with gray, but not
  // disabled.
  if (availabilityOf(r) === 'not_found') {
    return 'bg-gray-200 border-gray-500 text-gray-800 hover:bg-gray-300 disabled:opacity-60'
  }
  return 'bg-vibrant-pink border-vibrant-pink text-white hover:bg-vibrant-pink-light disabled:opacity-60 pixel-texture-vibrant'
}

async function handleQueue(r) {
  const id = keyFor(r)
  queuingId.value = id
  try {
    await queueStore.searchAndQueue({
      title: r.title,
      artist: r.artist,
      catalogNumber: r.catalogNumber || '',
      query: `${r.artist} — ${r.title}`,
    })
    await queueStore.fetchQueue(true)
  } finally {
    setTimeout(() => {
      if (queuingId.value === id) queuingId.value = null
    }, 1500)
  }
}
</script>
