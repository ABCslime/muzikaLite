import { defineStore } from 'pinia'
import { queueAPI } from '@/api/queue'

export const useQueueStore = defineStore('queue', {
  // Note: We'll import player store inside the method to avoid circular dependencies
  state: () => ({
    songs: [],
    loading: false,
    error: null,
    // v0.4 PR 3: remember the last user-initiated search so the UI can
    // display "searching for X…" feedback while the stub resolves.
    lastSearchQuery: null,
    lastSearchSongId: null,
    // v0.4.2 PR A: flip to true the first time the stub appears in
    // `songs` (probing or ready). Lets TopBar distinguish:
    //   never seen  -> still searching Discogs (keep "searching…" notice)
    //   seen, gone  -> backend auto-deleted on not_found (toast "sadly not found")
    lastSearchSawStub: false,
  }),

  getters: {
    songCount: (state) => state.songs.length,
  },

  actions: {
    async fetchQueue(skipAutoSelect = false) {
      console.group('🟢 Queue Store - fetchQueue()')
      console.log('⏰ Starting fetchQueue at:', new Date().toISOString())
      console.log('📊 Current State:', {
        songsCount: this.songs.length,
        loading: this.loading,
        error: this.error,
      })
      
      this.loading = true
      this.error = null
      
      try {
        console.log('📞 Calling queueAPI.getQueue()...')
        const response = await queueAPI.getQueue()
        
        console.log('📥 API Response received in store:')
        console.log('   Response:', response)
        console.log('   Response Type:', typeof response)
        console.log('   Has songs property:', 'songs' in (response || {}))
        console.log('   Songs value:', response?.songs)
        console.log('   Songs type:', typeof response?.songs)
        console.log('   Songs is array:', Array.isArray(response?.songs))
        
        // Preserve the exact order from the API response
        this.songs = response?.songs || []

        // v0.4.2 PR A: mark the last-searched stub as "seen" once it
        // shows up in the queue, even once (probing or ready). TopBar
        // uses this to tell "still Discogs-searching" (never seen)
        // apart from "backend auto-deleted on not_found" (seen, gone).
        if (this.lastSearchSongId) {
          const hit = this.songs.find(s => s.id === this.lastSearchSongId)
          if (hit) this.lastSearchSawStub = true
        }
        
        console.log('✅ Store updated:')
        console.log('   Songs count:', this.songs.length)
        console.log('   Songs:', this.songs)
        
        // Sync with player store - replace queue with API order to preserve ordering
        try {
          const { usePlayerStore } = await import('./player')
          const playerStore = usePlayerStore()
          
          // Update queue array directly with API order (preserves exact order from backend)
          playerStore.queue = [...this.songs]
          
          // Update currentIndex to point to the current playing song in the reordered queue
          if (playerStore.currentSong?.id) {
            const currentIndexInApiQueue = this.songs.findIndex(s => s.id === playerStore.currentSong.id)
            if (currentIndexInApiQueue >= 0) {
              playerStore.currentIndex = currentIndexInApiQueue
            }
          } else {
            // Only auto-select first unplayed song if not skipping (e.g., when just adding to queue)
            if (!skipAutoSelect && this.songs.length > 0) {
              playerStore.selectFirstUnplayedSong()
            }
          }
          
          console.log('🔄 Player store queue replaced with API queue (preserving order)')
        } catch (syncError) {
          console.warn('⚠️ Failed to sync with player store:', syncError)
          // Don't fail the fetchQueue operation if sync fails
        }
        
        const result = { success: true, data: this.songs }
        console.log('📤 Returning result:', result)
        console.groupEnd()
        return result
      } catch (error) {
        console.error('❌ Error in fetchQueue:')
        console.error('   Error:', error)
        console.error('   Error Type:', error.constructor.name)
        console.error('   Error Message:', error.message)
        console.error('   Response Status:', error.response?.status)
        console.error('   Response Data:', error.response?.data)
        
        const errorMessage = error.response?.data?.message || error.message || 'Failed to fetch queue'
        // If it's a 500 error, provide a helpful message
        if (error.response?.status === 500) {
          this.error = 'Queue service is unavailable. Please ensure QueueManager is running on port 8090.'
        } else if (error.response?.status === 403) {
          this.error = 'Queue service returned 403 Forbidden. Check CORS configuration on QueueManager.'
        } else {
          this.error = errorMessage
        }
        
        // Set empty queue on error so app doesn't break
        this.songs = []
        
        console.error('📊 Store state after error:')
        console.error('   Error:', this.error)
        console.error('   Songs:', this.songs)
        
        const result = { success: false, error: this.error }
        console.groupEnd()
        return result
      } finally {
        this.loading = false
        console.log('🏁 fetchQueue completed, loading set to false')
      }
    },

    async addSongToQueue(songId, position = null) {
      this.loading = true
      this.error = null
      try {
        const pos = position !== null ? position : this.songs.length
        await queueAPI.addSongToQueue(songId, pos)
        // Refresh queue after adding, but skip auto-selecting/playing the song
        await this.fetchQueue(true)
        return { success: true }
      } catch (error) {
        this.error = error.response?.data?.message || error.message || 'Failed to add song to queue'
        return { success: false, error: this.error }
      } finally {
        this.loading = false
      }
    },

    async removeSongFromQueue(songId) {
      this.loading = true
      this.error = null
      try {
        await queueAPI.removeSongFromQueue(songId)
        // Refresh queue after removing
        await this.fetchQueue()
        return { success: true }
      } catch (error) {
        this.error = error.response?.data?.message || error.message || 'Failed to remove song from queue'
        return { success: false, error: this.error }
      } finally {
        this.loading = false
      }
    },

    // v0.4.1 PR C: typeahead preview. Returns candidates without
    // queueing anything. No store state change — the caller (TopBar)
    // owns the dropdown state. Errors surface to caller for UX.
    async previewSearch(query) {
      try {
        const candidates = await queueAPI.previewSearch(query)
        return { success: true, data: candidates }
      } catch (error) {
        const status = error.response?.status
        const msg = error.response?.data?.message || error.message || 'Preview failed'
        return { success: false, error: msg, status }
      }
    },

    // v0.4.1 PR C: queue a specific (pre-picked) release OR fall back to
    // the legacy auto-pick path. Accepts either:
    //   {title, artist, catalogNumber?, query?} — pre-picked (preferred)
    //   {query}                                  — legacy auto-pick
    // Returns { songId, query } on success. lastSearchQuery/lastSearchSongId
    // drive the "searching for X…" banner until the entry appears.
    async searchAndQueue(candidate) {
      this.error = null
      try {
        const resp = await queueAPI.searchAndQueue(candidate)
        this.lastSearchQuery = resp.query || candidate.query || candidate.title
        this.lastSearchSongId = resp.songId
        // Reset the seen flag for the new search. fetchQueue flips it
        // true when it observes the stub.
        this.lastSearchSawStub = false
        return { success: true, data: resp }
      } catch (error) {
        this.error = error.response?.data?.message || error.message || 'Search failed'
        return { success: false, error: this.error }
      }
    },

    setSongs(songs) {
      this.songs = songs
    },

    clearError() {
      this.error = null
    },
  },
})

