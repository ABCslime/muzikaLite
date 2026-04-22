// v0.5 PR B — per-session similar-mode state.
//
// Shadows the backend's user_similarity_settings row. Backend
// is the source of truth; this store caches the active seed so
// the PlayerBar lens icon doesn't have to hit /api/queue/similar-mode
// on every frame, and so other components can react to changes
// without polling.
//
// Lifecycle:
//   - app boot: hydrate() pulls the current state from the backend
//     (the user might have left the mode on across a page reload)
//   - lens click: toggleForCurrentSong() flips state and calls the
//     backend; on error it rolls back so the icon doesn't lie
//   - song deleted from queue: backend's ON DELETE SET NULL
//     auto-clears server-side; the next hydrate() picks it up
//
// Deliberately NOT in stores/player.js: the player store is the
// hot path for audio + queue rendering and is already large.
// One file per concern.

import { defineStore } from 'pinia'
import { queueAPI } from '@/api/queue'

export const useSimilarStore = defineStore('similar', {
  state: () => ({
    seedSongId: null,
    // 'unknown' = haven't called the API yet (skeleton state on
    // first paint). 'on' / 'off' once we know.
    status: 'unknown',
    // Set true on the lens icon while a toggle round-trip is in
    // flight, so we can disable the button to prevent double-clicks.
    pending: false,
    // v0.5 PR E: when similar mode is on but the backend's last
    // NextPick failed (seed unknown to Discogs, or every bucket
    // came back empty), the server surfaces the reason here.
    // Empty string = last cycle succeeded. The PlayerBar uses
    // this to render the lens in an orange "active but not
    // working" state, distinct from both off and healthy-active.
    lastError: '',
  }),

  getters: {
    active: (state) => state.status === 'on' && !!state.seedSongId,
    hasError: (state) =>
      state.status === 'on' && !!state.seedSongId && state.lastError !== '',
  },

  actions: {
    // hydrate is fire-and-forget; failures collapse to status='off'
    // so the lens icon is always in a determinate visual state.
    async hydrate() {
      try {
        const r = await queueAPI.getSimilarMode()
        this.seedSongId = r.seedSongId || null
        this.status = r.active ? 'on' : 'off'
        this.lastError = r.lastError || ''
      } catch {
        this.seedSongId = null
        this.status = 'off'
        this.lastError = ''
      }
    },

    // toggleForSong is the click handler. If we're currently OFF
    // (or seeded by a different song), turn ON with the given
    // songId. If we're ON with this exact songId, turn OFF.
    //
    // The "different song" case is intentional: clicking the lens
    // while playing track B when the seed is track A should
    // RE-SEED to B, not toggle off. Matches the user's mental
    // model — "make the queue follow this one."
    async toggleForSong(songId) {
      if (!songId) return
      if (this.pending) return
      this.pending = true
      const wasActive = this.active
      const wasSameSeed = this.seedSongId === songId
      // Optimistic local update so the lens icon flips immediately.
      const nextSeed = wasActive && wasSameSeed ? null : songId
      const prevSeed = this.seedSongId
      const prevStatus = this.status
      const prevError = this.lastError
      this.seedSongId = nextSeed
      this.status = nextSeed ? 'on' : 'off'
      // Changing the seed invalidates any prior error — the backend
      // clears it server-side too; mirror here for snappy UX.
      this.lastError = ''
      try {
        await queueAPI.setSimilarMode(nextSeed)
      } catch {
        // Roll back so we're not lying to the user.
        this.seedSongId = prevSeed
        this.status = prevStatus
        this.lastError = prevError
      } finally {
        this.pending = false
      }
    },

    // clear() unconditionally turns the mode off. Useful when a
    // future PR adds a "stop similar mode" affordance separate
    // from the per-song toggle.
    async clear() {
      if (this.pending) return
      this.pending = true
      const prevSeed = this.seedSongId
      const prevStatus = this.status
      const prevError = this.lastError
      this.seedSongId = null
      this.status = 'off'
      this.lastError = ''
      try {
        await queueAPI.setSimilarMode(null)
      } catch {
        this.seedSongId = prevSeed
        this.status = prevStatus
        this.lastError = prevError
      } finally {
        this.pending = false
      }
    },
  },
})
