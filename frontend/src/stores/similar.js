// v0.5 PR B + v0.6 PR D — similar-mode state.
//
// The store caches the full seed set with metadata so the
// PlayerBar lens and Home-view pins can render without re-
// hitting the backend on every frame. Backend is the source of
// truth; the store mirrors it with optimistic-update shape for
// click responsiveness and rollback on error.
//
// Lifecycle:
//   - boot: hydrate() pulls the current state (multi-seed)
//   - per-song lens click: adds/removes THAT song to/from the
//     seed set. Doesn't clear the set just because you tapped
//     something else — the "+ another seed" UX.
//   - Home pin × click: removes that specific seed; if it was
//     the last, similar mode turns off automatically.
//
// Deliberately NOT merged into stores/player.js: player is the
// audio hot path and grows often; similar state is orthogonal.

import { defineStore } from 'pinia'
import { queueAPI } from '@/api/queue'

export const useSimilarStore = defineStore('similar', {
  state: () => ({
    // v0.6: full seed set. Each entry {id, title, artist}.
    // Order matches the backend's stable ordering.
    seeds: [],
    // 'unknown' | 'on' | 'off'. 'on' iff seeds.length > 0.
    status: 'unknown',
    // True while a mutation round-trip is in flight; disables the
    // relevant buttons so double-clicks don't fire duplicate
    // requests. Backend mutations are idempotent so double-
    // firing would be correct, but we avoid the noise.
    pending: false,
    // Backend's most recent NextPick failure reason. Empty when
    // the last cycle succeeded; drives the amber lens state.
    lastError: '',
  }),

  getters: {
    active: (state) => state.seeds.length > 0 && state.status === 'on',
    hasError: (state) =>
      state.seeds.length > 0 && state.status === 'on' && state.lastError !== '',

    // True iff songId is currently in the seed set. Lens uses
    // this to decide between "+ add" and "× remove" visuals.
    hasSeed: (state) => (songId) =>
      state.seeds.some((s) => s.id === songId),
  },

  actions: {
    // hydrate is fire-and-forget; failures collapse to the empty-
    // state so the UI always has a determinate set to render.
    async hydrate() {
      try {
        const r = await queueAPI.getSimilarMode()
        this._applyServerState(r)
      } catch {
        this.seeds = []
        this.status = 'off'
        this.lastError = ''
      }
    },

    // toggleSeed is the lens click handler. If the given song
    // is ALREADY a seed, remove it; otherwise add it. Single-
    // round-trip per click.
    //
    // Optional {title, artist} args let the caller supply the
    // metadata from the player store so the chip on Home pops
    // in immediately — backend will echo the same values but
    // with a round-trip of latency.
    async toggleSeed(songId, { title = '', artist = '' } = {}) {
      if (!songId || this.pending) return
      this.pending = true
      const wasSeed = this.seeds.some((s) => s.id === songId)
      // Optimistic local update.
      const prev = this._snapshot()
      if (wasSeed) {
        this.seeds = this.seeds.filter((s) => s.id !== songId)
      } else {
        this.seeds = [...this.seeds, { id: songId, title, artist }]
      }
      this.status = this.seeds.length > 0 ? 'on' : 'off'
      this.lastError = ''
      try {
        const r = wasSeed
          ? await queueAPI.removeSimilarSeed(songId)
          : await queueAPI.addSimilarSeed(songId)
        this._applyServerState(r)
      } catch {
        this._restore(prev)
      } finally {
        this.pending = false
      }
    },

    // removeSeed is the Home-pin × handler. Separate from
    // toggleSeed so pin clicks don't accidentally re-add a song
    // when its id happens to not be in the set (shouldn't happen,
    // but the explicit path removes the branch entirely).
    async removeSeed(songId) {
      if (!songId || this.pending) return
      this.pending = true
      const prev = this._snapshot()
      this.seeds = this.seeds.filter((s) => s.id !== songId)
      this.status = this.seeds.length > 0 ? 'on' : 'off'
      this.lastError = ''
      try {
        const r = await queueAPI.removeSimilarSeed(songId)
        this._applyServerState(r)
      } catch {
        this._restore(prev)
      } finally {
        this.pending = false
      }
    },

    // clear drops all seeds in one round-trip. Used by bulk
    // "stop similar mode" affordances (future — not wired in
    // v0.6 UI, but the API is here).
    async clear() {
      if (this.pending) return
      this.pending = true
      const prev = this._snapshot()
      this.seeds = []
      this.status = 'off'
      this.lastError = ''
      try {
        await queueAPI.setSimilarMode(null)
      } catch {
        this._restore(prev)
      } finally {
        this.pending = false
      }
    },

    // _applyServerState mirrors a fresh GET/mutation response
    // into the store. Single place that knows the response
    // shape, so a wire-format change lands here only.
    _applyServerState(r) {
      this.seeds = Array.isArray(r?.seeds) ? r.seeds : []
      this.status = r?.active ? 'on' : 'off'
      this.lastError = r?.lastError || ''
    },

    _snapshot() {
      return {
        seeds: [...this.seeds],
        status: this.status,
        lastError: this.lastError,
      }
    },
    _restore(s) {
      this.seeds = s.seeds
      this.status = s.status
      this.lastError = s.lastError
    },
  },
})
