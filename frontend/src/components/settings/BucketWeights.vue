<template>
  <!-- v0.5 PR D: per-bucket weight tuning for similar-mode
       discovery. One slider per registered bucket; sliders are
       generated from the backend's bucket registry so adding a
       new bucket (v0.6 plugin, v0.7 events) shows up here with
       no frontend change. -->
  <section>
    <h2 class="text-xl font-bold text-gray-900 mb-2">Discovery weights</h2>
    <p class="text-sm text-gray-600 mb-3">
      When similar mode is on, these weights decide how much each
      bucket contributes. Set to 0 to disable a bucket; raise it
      to make its picks dominate. Defaults are a starting point —
      tune to taste.
    </p>

    <div v-if="loading" class="text-gray-600 py-4">Loading discovery weights…</div>
    <div
      v-else-if="error"
      class="bg-red-100 pixel-border border-red-500 p-3 text-red-700 pixel-texture mb-4"
    >
      {{ error }}
    </div>
    <div
      v-else-if="!buckets.length"
      class="bg-amber-50 pixel-border border-amber-500 p-3 text-amber-800 pixel-texture"
    >
      No similarity buckets registered. Similar mode falls back to
      random genre-based refill for this user. Check that
      <code class="text-vibrant-pink">MUZIKA_DISCOGS_ENABLED=true</code>
      in the server config.
    </div>

    <!-- v0.6 PR F: preset picker. Sits above the sliders —
         clicking Apply updates the local draft, user fine-tunes
         from there, Save persists. Presets themselves are
         read-only, defined server-side. -->
    <div
      v-if="!loading && !error && buckets.length && presets.length"
      class="pixel-border border-gray-400 bg-white p-3 pixel-texture mb-4 flex items-center gap-3 flex-wrap"
    >
      <label class="text-sm font-semibold text-gray-900">Start from preset:</label>
      <select
        v-model="selectedPresetId"
        class="px-2 py-1 pixel-border border-gray-500 bg-white text-sm text-gray-900"
      >
        <option value="">— pick a preset —</option>
        <option v-for="p in presets" :key="p.id" :value="p.id">
          {{ p.label }}
        </option>
      </select>
      <button
        type="button"
        :disabled="!selectedPresetId"
        @click="applyPreset"
        class="px-3 py-1 pixel-border text-xs font-semibold bg-vibrant-purple-light border-vibrant-purple text-gray-900 hover:bg-vibrant-purple hover:text-white disabled:opacity-50"
      >
        Apply
      </button>
      <p v-if="selectedPresetDescription" class="w-full text-xs text-gray-600 italic">
        {{ selectedPresetDescription }}
      </p>
    </div>

    <div v-if="!loading && !error && buckets.length" class="space-y-4">
      <div
        v-for="b in buckets"
        :key="b.id"
        class="pixel-border border-gray-400 bg-white p-3 pixel-texture"
      >
        <div class="flex items-center justify-between mb-1">
          <div class="min-w-0 flex-1">
            <div class="font-semibold text-gray-900">{{ b.label }}</div>
            <div class="text-xs text-gray-600 mt-0.5">{{ b.description }}</div>
            <div class="text-xs text-gray-500 mt-0.5 font-mono">{{ b.id }}</div>
          </div>
          <div class="flex items-center gap-2 ml-4 flex-shrink-0">
            <span
              class="text-sm font-mono text-gray-900 w-10 text-right"
            >{{ effectiveWeight(b).toFixed(1) }}</span>
            <button
              type="button"
              @click="resetOne(b)"
              :title="`Reset to default (${b.defaultWeight.toFixed(1)})`"
              class="text-xs text-gray-500 hover:text-vibrant-pink underline"
            >
              reset
            </button>
          </div>
        </div>
        <input
          type="range"
          min="0"
          max="10"
          step="0.5"
          :value="effectiveWeight(b)"
          @input="setWeight(b.id, Number($event.target.value))"
          class="w-full accent-vibrant-pink"
        />
      </div>

      <div class="flex items-center gap-3 pt-2">
        <button
          type="button"
          :disabled="saving || noChanges"
          @click="save"
          class="px-5 py-2 bg-vibrant-pink text-white pixel-button border-vibrant-pink font-semibold hover:bg-vibrant-pink-light transition-colors pixel-texture-vibrant disabled:opacity-50"
        >
          {{ saving ? 'Saving…' : 'Save weights' }}
        </button>
        <button
          type="button"
          @click="resetAll"
          class="px-5 py-2 bg-gray-200 text-gray-900 pixel-button border-gray-500 font-semibold hover:bg-gray-300 transition-colors"
        >
          Reset all to defaults
        </button>
        <span
          v-if="savedNotice"
          class="text-sm text-green-700"
        >{{ savedNotice }}</span>
      </div>
    </div>
  </section>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { similarityAPI } from '@/api/similarity'

// ----- state -----
const buckets = ref([]) // [{id,label,description,defaultWeight}, ...]
const loading = ref(true)
const error = ref('')

// draft[bucketID] → number. A bucket id absent from `draft` means
// "use the default." The backend stores the same sparse-map shape.
const draft = ref({})
// snapshot of what we loaded from the backend, so "noChanges"
// can tell if the user touched anything.
const loaded = ref({})

const saving = ref(false)
const savedNotice = ref('')

// v0.6 PR F — preset state.
const presets = ref([])             // [{id,label,description,weights}, ...]
const selectedPresetId = ref('')    // empty = "no preset picked yet"

// ----- derived -----
function effectiveWeight(b) {
  const v = draft.value[b.id]
  return typeof v === 'number' ? v : b.defaultWeight
}

const noChanges = computed(() => {
  // Compare key sets + values.
  const keys = new Set([
    ...Object.keys(draft.value),
    ...Object.keys(loaded.value),
  ])
  for (const k of keys) {
    if (draft.value[k] !== loaded.value[k]) return false
  }
  return true
})

const selectedPresetDescription = computed(() => {
  const p = presets.value.find((x) => x.id === selectedPresetId.value)
  return p?.description || ''
})

// ----- actions -----
function setWeight(id, value) {
  if (!Number.isFinite(value) || value < 0) value = 0
  draft.value = { ...draft.value, [id]: value }
  savedNotice.value = ''
}

function resetOne(b) {
  const next = { ...draft.value }
  delete next[b.id]
  draft.value = next
  savedNotice.value = ''
}

function resetAll() {
  draft.value = {}
  savedNotice.value = ''
}

// applyPreset loads the selected preset's weights into the
// draft (not the backend — save still requires an explicit
// click). Only copies keys for buckets that are currently
// registered: a preset that references a plugin bucket that
// hasn't loaded silently ignores those keys, so the user
// doesn't end up with "phantom" entries in their tuned map.
function applyPreset() {
  const p = presets.value.find((x) => x.id === selectedPresetId.value)
  if (!p) return
  const registered = new Set(buckets.value.map((b) => b.id))
  const next = {}
  for (const [k, v] of Object.entries(p.weights || {})) {
    if (registered.has(k)) next[k] = Number(v)
  }
  draft.value = next
  savedNotice.value = ''
}

async function save() {
  saving.value = true
  error.value = ''
  try {
    const returned = await similarityAPI.setWeights(draft.value)
    // Backend canonicalizes (clamps negatives to 0); reflect
    // what it stored as the new "loaded" snapshot.
    loaded.value = { ...returned }
    draft.value = { ...returned }
    savedNotice.value = 'Saved.'
    setTimeout(() => { savedNotice.value = '' }, 2000)
  } catch (e) {
    error.value = e.response?.data?.message || e.message || 'Save failed.'
  } finally {
    saving.value = false
  }
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [bs, w, ps] = await Promise.all([
      similarityAPI.listBuckets(),
      similarityAPI.getWeights(),
      similarityAPI.listPresets(),
    ])
    buckets.value = bs || []
    loaded.value = w || {}
    draft.value = { ...(w || {}) }
    presets.value = ps || []
  } catch (e) {
    error.value = e.response?.data?.message || e.message || 'Failed to load bucket settings.'
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>
