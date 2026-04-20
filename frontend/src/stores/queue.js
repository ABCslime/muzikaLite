import { defineStore } from 'pinia'
import { queueAPI } from '@/api/queue'

export const useQueueStore = defineStore('queue', {
  // Note: We'll import player store inside the method to avoid circular dependencies
  state: () => ({
    songs: [],
    loading: false,
    error: null,
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

    setSongs(songs) {
      this.songs = songs
    },

    clearError() {
      this.error = null
    },
  },
})

