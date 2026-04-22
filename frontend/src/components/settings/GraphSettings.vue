<template>
  <!-- v0.7 PR C: per-user tuning for the Discovery graph tab.
       Currently just node count; future releases can add more
       settings here (layout kind, edge-color overrides, etc.)
       without reshaping the panel. -->
  <section>
    <h2 class="text-xl font-bold text-gray-900 mb-2">Discovery graph</h2>
    <p class="text-sm text-gray-600 mb-3">
      How many neighbor songs appear around the currently playing
      song on the graph tab. More nodes = more exploration, fewer
      = clearer view.
    </p>

    <div v-if="loading" class="text-gray-600 py-4">Loading graph settings…</div>
    <div
      v-else-if="error"
      class="bg-red-100 pixel-border border-red-500 p-3 text-red-700 pixel-texture mb-4"
    >
      {{ error }}
    </div>

    <div
      v-else
      class="pixel-border border-gray-400 bg-white p-3 pixel-texture"
    >
      <div class="flex items-center justify-between mb-1">
        <div class="min-w-0 flex-1">
          <div class="font-semibold text-gray-900">Neighbor node count</div>
          <div class="text-xs text-gray-600 mt-0.5">
            Drawn from the same-label and collaborators buckets in
            an equal split (with fallback fill if one is shorter).
          </div>
        </div>
        <div class="flex items-center gap-2 ml-4 flex-shrink-0">
          <span
            class="text-sm font-mono text-gray-900 w-10 text-right"
          >{{ draft }}</span>
          <button
            type="button"
            @click="resetToDefault"
            :title="`Reset to default (${defaultNodeLimit})`"
            class="text-xs text-gray-500 hover:text-vibrant-pink underline"
          >
            reset
          </button>
        </div>
      </div>
      <input
        type="range"
        :min="1"
        :max="maxNodeLimit"
        step="1"
        v-model.number="draft"
        class="w-full accent-vibrant-pink"
      />
      <div class="flex items-center gap-3 pt-3">
        <button
          type="button"
          :disabled="saving || draft === loaded"
          @click="save"
          class="px-5 py-2 bg-vibrant-pink text-white pixel-button border-vibrant-pink font-semibold hover:bg-vibrant-pink-light transition-colors pixel-texture-vibrant disabled:opacity-50"
        >
          {{ saving ? 'Saving…' : 'Save' }}
        </button>
        <span v-if="savedNotice" class="text-sm text-green-700">{{ savedNotice }}</span>
      </div>
    </div>
  </section>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { similarityAPI } from '@/api/similarity'

const loading = ref(true)
const error = ref('')
const saving = ref(false)
const savedNotice = ref('')

// draft = slider-controlled; loaded = last-saved state (for the
// Save-button disabled state).
const draft = ref(8)
const loaded = ref(8)
const defaultNodeLimit = ref(8)
const maxNodeLimit = ref(30)

function resetToDefault() {
  draft.value = defaultNodeLimit.value
  savedNotice.value = ''
}

async function save() {
  saving.value = true
  error.value = ''
  try {
    const r = await similarityAPI.setGraphSettings({ nodeLimit: draft.value })
    loaded.value = r.nodeLimit
    draft.value = r.nodeLimit
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
    const r = await similarityAPI.getGraphSettings()
    loaded.value = r.nodeLimit
    draft.value = r.nodeLimit
    defaultNodeLimit.value = r.defaultNodeLimit || 8
    maxNodeLimit.value = r.maxNodeLimit || 30
  } catch (e) {
    error.value = e.response?.data?.message || e.message || 'Failed to load graph settings.'
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>
