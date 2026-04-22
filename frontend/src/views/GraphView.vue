<template>
  <div class="flex h-screen">
    <Sidebar />
    <div class="flex-1 flex flex-col overflow-hidden">
      <TopBar />
      <div class="flex-1 overflow-hidden p-4 flex flex-col">
        <div class="flex items-center justify-between mb-3">
          <h1 class="text-2xl font-bold text-gray-900">Discovery graph</h1>
          <p class="text-xs text-gray-600">
            Neighbors of the currently playing song. Blue = same label. Pink = collaborators.
          </p>
        </div>

        <!-- Empty states cover three cases: no song playing, no
             Discogs match, and no connections. Each prints its
             own guidance rather than blanking the canvas. -->
        <div
          v-if="!playerStore.currentSong"
          class="flex-1 flex items-center justify-center"
        >
          <div class="text-center text-gray-600 max-w-md">
            <p class="text-lg font-semibold mb-2">No song playing</p>
            <p class="text-sm">
              Play a song to see its discovery graph — same-label releases
              and collaborator releases will appear around it.
            </p>
          </div>
        </div>
        <div
          v-else-if="loading"
          class="flex-1 flex items-center justify-center text-gray-600"
        >
          Loading graph…
        </div>
        <div
          v-else-if="error"
          class="flex-1 flex items-center justify-center"
        >
          <div class="bg-red-100 pixel-border border-red-500 p-4 text-red-700 pixel-texture">
            {{ error }}
          </div>
        </div>
        <div
          v-else-if="graph && graph.edges.length === 0"
          class="flex-1 flex items-center justify-center"
        >
          <div class="text-center text-gray-600 max-w-md">
            <p class="text-lg font-semibold mb-2">No connections found</p>
            <p class="text-sm">
              This track doesn't have neighbors on Discogs we can chart yet
              (no shared label, no credited collaborators). Play a more
              well-documented release to populate the graph.
            </p>
          </div>
        </div>

        <!-- Canvas. Explicitly sized so Cytoscape can compute its
             layout — the library requires a non-zero-bounding
             parent at mount time. flex-1 handles the rest. -->
        <div
          v-show="graph && graph.edges.length > 0"
          ref="canvasEl"
          class="flex-1 pixel-border border-gray-400 bg-white relative"
        ></div>
      </div>
      <PlayerBar />
    </div>
  </div>
</template>

<script setup>
import { ref, watch, onMounted, onUnmounted, nextTick } from 'vue'
import Sidebar from '@/components/layout/Sidebar.vue'
import TopBar from '@/components/layout/TopBar.vue'
import PlayerBar from '@/components/layout/PlayerBar.vue'
import { similarityAPI } from '@/api/similarity'
import { usePlayerStore } from '@/stores/player'

const playerStore = usePlayerStore()

// Canvas element + Cytoscape instance. We import cytoscape
// lazily (onMounted) so the ~300 kb bundle stays out of the
// base chunk — only loaded when the user actually visits /graph.
const canvasEl = ref(null)
let cy = null // cytoscape Core instance; not reactive

const graph = ref(null) // {center, nodes, edges}
const loading = ref(false)
const error = ref('')

// v0.7 default. PR C will let users persist this per user.
const nodeLimit = 8

async function loadGraph(songId) {
  if (!songId) {
    graph.value = null
    return
  }
  loading.value = true
  error.value = ''
  try {
    const data = await similarityAPI.getGraph(songId, nodeLimit)
    graph.value = data
    await nextTick()
    renderGraph()
  } catch (e) {
    error.value = e.response?.data?.message || e.message || 'Failed to load graph.'
    graph.value = null
  } finally {
    loading.value = false
  }
}

// renderGraph pushes the current `graph` state into Cytoscape.
// Called after loadGraph and whenever we need to refresh (e.g.,
// currentSong changed → loadGraph → renderGraph).
//
// Uses Cytoscape's `concentric` layout — puts the center at
// the middle and arranges neighbors on one ring. Matches the
// star-layout intent and handles varying node counts cleanly.
function renderGraph() {
  if (!cy || !graph.value || !canvasEl.value) return
  const g = graph.value
  const elements = []
  // Center node is always at Nodes[0] per the backend contract.
  for (const n of g.nodes) {
    elements.push({
      group: 'nodes',
      data: {
        id: n.id,
        label: n.title ? `${n.artist || ''} — ${n.title}` : n.id,
        artist: n.artist || '',
        title: n.title || '',
        isCenter: !!n.isCenter,
        imageUrl: n.imageUrl || '',
      },
    })
  }
  for (const e of g.edges) {
    elements.push({
      group: 'edges',
      data: {
        id: `${e.source}->${e.target}:${e.bucket}`,
        source: e.source,
        target: e.target,
        bucket: e.bucket,
      },
    })
  }
  cy.elements().remove()
  cy.add(elements)
  // Resize before layout: the canvas may have been 0×0 when
  // Cytoscape initialized (we use v-show to toggle the
  // container visibility based on graph.edges.length, which
  // means cytoscape first measured zero). resize() re-reads
  // the current container dimensions; without it the
  // concentric layout crams everything into the top-left.
  cy.resize()
  cy.layout({
    name: 'concentric',
    concentric: (node) => (node.data('isCenter') ? 2 : 1),
    levelWidth: () => 1,
    minNodeSpacing: 40,
    startAngle: -Math.PI / 2, // start neighbors at 12 o'clock
    animate: false,
  }).run()
  cy.fit(undefined, 40) // 40px padding around the graph
}

onMounted(async () => {
  // Lazy-load cytoscape — keeps it out of the base bundle.
  const cytoscapeMod = await import('cytoscape')
  const cytoscape = cytoscapeMod.default || cytoscapeMod

  cy = cytoscape({
    container: canvasEl.value,
    elements: [],
    // v0.7 color mapping: blue for discogs.same_label_era,
    // pink for discogs.collaborators. Future buckets (events,
    // plugins) get their own colors in later versions.
    style: [
      {
        selector: 'node',
        style: {
          'background-color': '#c4b5fd', // vibrant purple light
          'border-width': 2,
          'border-color': '#7c3aed',
          label: 'data(label)',
          'text-valign': 'bottom',
          'text-halign': 'center',
          'text-margin-y': 6,
          'font-size': 11,
          color: '#1f2937',
          'text-outline-width': 2,
          'text-outline-color': '#fdf2f8',
          width: 36,
          height: 36,
          'text-wrap': 'ellipsis',
          'text-max-width': 140,
        },
      },
      {
        selector: 'node[?isCenter]',
        style: {
          'background-color': '#ec4899', // vibrant pink
          'border-color': '#831843',
          'border-width': 3,
          width: 56,
          height: 56,
          'font-size': 13,
          'font-weight': 'bold',
        },
      },
      {
        selector: 'edge',
        style: {
          width: 2,
          'curve-style': 'bezier',
          'target-arrow-shape': 'none',
          opacity: 0.8,
        },
      },
      {
        selector: 'edge[bucket = "discogs.same_label_era"]',
        style: {
          'line-color': '#38bdf8', // vibrant sky blue
        },
      },
      {
        selector: 'edge[bucket = "discogs.collaborators"]',
        style: {
          'line-color': '#ec4899', // vibrant pink
          'line-style': 'dashed',
        },
      },
    ],
  })

  // Initial render + reactive refresh whenever the center
  // song changes. watch with immediate: true fires once at
  // mount with the current id.
  watch(
    () => playerStore.currentSong?.id,
    (id) => {
      loadGraph(id)
    },
    { immediate: true },
  )
})

onUnmounted(() => {
  if (cy) {
    cy.destroy()
    cy = null
  }
})
</script>
