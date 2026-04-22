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
             parent at mount time. flex-1 handles the rest.
             Positioned relative so the tooltip overlay can
             anchor absolutely inside it. -->
        <div
          v-show="graph && graph.edges.length > 0"
          ref="canvasEl"
          class="flex-1 pixel-border border-gray-400 bg-white relative"
        >
          <!-- Hover tooltip: bucket label on edges, full
               artist/title on nodes. Positioned manually from
               Cytoscape's renderedPosition + the canvas bounding
               box — Cytoscape doesn't ship a built-in tooltip. -->
          <div
            v-show="tooltip.visible"
            class="absolute z-50 pointer-events-none pixel-border border-gray-700 bg-white text-xs text-gray-900 px-2 py-1 shadow-md"
            :style="{ left: tooltip.x + 'px', top: tooltip.y + 'px' }"
          >
            {{ tooltip.text }}
          </div>
          <!-- Queueing feedback: when user clicks a neighbor we
               may wait a few seconds for the new song to
               download. A non-blocking banner beats a frozen
               click. -->
          <div
            v-if="queueingMessage"
            class="absolute bottom-3 left-3 pixel-border border-vibrant-pink bg-pinkish-white px-3 py-2 text-xs text-gray-900 pixel-texture"
          >
            {{ queueingMessage }}
          </div>
        </div>
      </div>
      <PlayerBar />
    </div>
  </div>
</template>

<script setup>
import { ref, watch, onMounted, onUnmounted, nextTick, reactive } from 'vue'
import Sidebar from '@/components/layout/Sidebar.vue'
import TopBar from '@/components/layout/TopBar.vue'
import PlayerBar from '@/components/layout/PlayerBar.vue'
import { similarityAPI } from '@/api/similarity'
import { usePlayerStore } from '@/stores/player'
import { useQueueStore } from '@/stores/queue'

const playerStore = usePlayerStore()
const queueStore = useQueueStore()

// v0.7 PR B: bucket-id → human-readable label for the hover
// tooltip. Kept here rather than in the backend response so
// the frontend can rename without a wire change.
const bucketLabels = {
  'discogs.same_label_era': 'Same label, similar era',
  'discogs.collaborators': 'Featured collaborator',
  // Future buckets (events, plugins) get their labels added
  // here as they ship.
}

// Tooltip + queueing state for hover + click UX.
const tooltip = reactive({ visible: false, x: 0, y: 0, text: '' })
const queueingMessage = ref('')

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

  // Node click: play the clicked song + recenter the graph.
  // Pure exploration. Center node click is a no-op.
  cy.on('tap', 'node', async (evt) => {
    const node = evt.target
    if (node.data('isCenter')) return
    await handleCandidateClick({
      title: node.data('title'),
      artist: node.data('artist'),
      imageUrl: node.data('imageUrl'),
    })
  })

  // Hover tooltips — nodes show "artist — title", edges show
  // the bucket's human label. We position the tooltip in
  // container-relative coords by subtracting the canvas bbox
  // from the screen-space mouse event.
  cy.on('mouseover', 'node', (evt) => {
    const n = evt.target
    const artist = n.data('artist') || ''
    const title = n.data('title') || ''
    tooltip.text = (artist && title) ? `${artist} — ${title}` : (title || artist || '')
    positionTooltip(evt.originalEvent)
    tooltip.visible = true
  })
  cy.on('mouseover', 'edge', (evt) => {
    const bucket = evt.target.data('bucket') || ''
    tooltip.text = bucketLabels[bucket] || bucket
    positionTooltip(evt.originalEvent)
    tooltip.visible = true
  })
  cy.on('mouseout', 'node edge', () => {
    tooltip.visible = false
  })
  cy.on('mousemove', (evt) => {
    if (tooltip.visible && evt.originalEvent) {
      positionTooltip(evt.originalEvent)
    }
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

// positionTooltip converts a screen-space MouseEvent into
// container-relative coords. The overlay div is absolutely
// positioned inside canvasEl, so we subtract the canvas
// bounding rect's origin.
function positionTooltip(ev) {
  if (!canvasEl.value || !ev) return
  const rect = canvasEl.value.getBoundingClientRect()
  tooltip.x = ev.clientX - rect.left + 12
  tooltip.y = ev.clientY - rect.top + 12
}

// handleCandidateClick kicks off the existing search-acquire
// flow for the clicked (title, artist). The pipeline:
//   1. POST /api/queue/search with {title, artist, imageUrl}
//      — backend inserts a queue stub (or reuses an existing
//      one via FindSongForReuse), returns the songID.
//   2. Optimistically recenter the graph on the new songID so
//      the UI reacts instantly (neighbor edges will populate
//      once Discogs hydration finishes).
//   3. Poll queueStore.songs for the songID reaching status=
//      ready. When it does, playerStore.play(song) starts
//      audio. The currentSong watcher in onMounted will then
//      refresh the graph with correct metadata.
//   4. If it never reaches ready within ~90s, clear the
//      queueing banner — the user can still play the song
//      manually from their queue.
async function handleCandidateClick({ title, artist, imageUrl }) {
  if (!title || !artist) return
  queueingMessage.value = `Queueing "${artist} — ${title}"…`
  let songId = ''
  try {
    const r = await queueStore.searchAndQueue({
      title,
      artist,
      imageUrl: imageUrl || '',
      query: `${artist} — ${title}`,
    })
    if (!r.success) {
      queueingMessage.value = `Couldn't queue: ${r.error || 'unknown error'}`
      setTimeout(() => { queueingMessage.value = '' }, 4000)
      return
    }
    songId = r.data?.songId || ''
    if (!songId) {
      queueingMessage.value = 'Queued but no song id returned'
      setTimeout(() => { queueingMessage.value = '' }, 4000)
      return
    }
    // Recenter graph immediately on the new songID. If
    // hydration hasn't landed yet the graph will render with
    // just a center; once metadata arrives a later refresh
    // fills in the neighbors.
    loadGraph(songId)
    // Refresh the queue store so the newly-queued song is in
    // queueStore.songs — otherwise the polling below won't see
    // it arrive.
    await queueStore.fetchQueue(true)
  } catch (e) {
    queueingMessage.value = `Couldn't queue: ${e.message || 'unknown error'}`
    setTimeout(() => { queueingMessage.value = '' }, 4000)
    return
  }

  // Poll the queue for the song becoming ready; when it does,
  // play it. Poll budget is ~90 seconds — a typical Soulseek
  // download takes 10-60s. After that we give up silently.
  const deadline = Date.now() + 90_000
  const checkReady = () => {
    const song = queueStore.songs.find((s) => s.id === songId)
    if (song && song.status === 'ready') {
      playerStore.play(song)
      queueingMessage.value = ''
      return true
    }
    return false
  }
  if (checkReady()) return
  const interval = setInterval(async () => {
    if (Date.now() > deadline) {
      clearInterval(interval)
      queueingMessage.value = ''
      return
    }
    // Pull a fresh queue listing; most of the data is already
    // polled by other surfaces, but this is the one action
    // that specifically cares about a *just-queued* song.
    await queueStore.fetchQueue(true)
    if (checkReady()) {
      clearInterval(interval)
    }
  }, 3000)
}

onUnmounted(() => {
  if (cy) {
    cy.destroy()
    cy = null
  }
})
</script>
