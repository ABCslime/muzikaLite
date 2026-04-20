import { defineStore } from 'pinia'
import { songsAPI } from '@/api/songs'
import { queueAPI } from '@/api/queue'

export const usePlayerStore = defineStore('player', {
  state: () => ({
    currentSong: null,
    isPlaying: false,
    currentTime: 0,
    duration: 0,
    volume: 0.5,
    queue: [],
    currentIndex: -1,
    shuffle: false,
    repeat: false, // false, 'one', 'all'
    audioElement: null,
    currentObjectUrl: null, // Store object URL for cleanup
    isSongEndingNaturally: false, // Track if song is ending naturally vs being skipped
    isNavigating: false, // Track if we're navigating (next/previous) to prevent duplicate skip calls
    isLiked: false, // Track if current song is liked
  }),

  getters: {
    hasNext: (state) => {
      if (state.repeat === 'all') return state.queue.length > 0
      return state.currentIndex < state.queue.length - 1
    },
    hasPrevious: (state) => {
      if (state.repeat === 'all') return state.queue.length > 0
      return state.currentIndex > 0
    },
    progress: (state) => {
      if (!state.duration) return 0
      return (state.currentTime / state.duration) * 100
    },
  },

  actions: {
    // Fetch liked status for a song
    async fetchLikedStatus(songId) {
      if (!songId) {
        this.isLiked = false
        return
      }
      
      try {
        const response = await songsAPI.getSongLikedStatus(songId)
        this.isLiked = response.liked || false
      } catch (error) {
        // Error already logged in API method
        // Default to false on error
        this.isLiked = false
      }
    },

    // Toggle liked status for current song
    async toggleLiked() {
      if (!this.currentSong?.id) {
        return
      }
      
      const songId = this.currentSong.id
      const wasLiked = this.isLiked
      
      // Optimistically update UI
      this.isLiked = !wasLiked
      
      try {
        if (wasLiked) {
          await songsAPI.markSongAsUnliked(songId)
        } else {
          await songsAPI.markSongAsLiked(songId)
        }
        // State already updated optimistically
      } catch (error) {
        // Revert on error
        this.isLiked = wasLiked
        console.error('Failed to toggle liked status:', error)
      }
    },

    // Helper method to mark song as skipped (non-blocking)
    async markCurrentSongAsSkipped() {
      if (!this.currentSong?.id || this.isSongEndingNaturally) {
        return
      }
      
      const songId = this.currentSong.id
      
      try {
        await queueAPI.markSongAsSkipped(songId)
        // Optionally refresh queue after skip
        this.refreshQueueAfterEvent()
      } catch (error) {
        // Error already logged in API method, just continue
        // Don't interrupt playback flow
      }
    },

    // Helper method to mark song as finished (non-blocking)
    async markCurrentSongAsFinished() {
      if (!this.currentSong?.id) {
        return
      }
      
      const songId = this.currentSong.id
      
      try {
        await queueAPI.markSongAsFinished(songId)
        // Optionally refresh queue after finish
        this.refreshQueueAfterEvent()
      } catch (error) {
        // Error already logged in API method, just continue
        // Don't interrupt playback flow
      }
    },

    // Helper method to refresh queue after skip/finish events (non-blocking)
    async refreshQueueAfterEvent() {
      try {
        const { useQueueStore } = await import('./queue')
        const queueStore = useQueueStore()
        // Don't await - let it run in background
        queueStore.fetchQueue().catch(() => {
          // Silently fail - queue refresh is optional
        })
      } catch (error) {
        // Silently fail - queue refresh is optional
      }
    },

    // Helper method to select the first unplayed song in the queue
    selectFirstUnplayedSong() {
      if (this.queue.length === 0) {
        return null
      }
      
      // Don't override if a song is already selected
      if (this.currentSong?.id) {
        return this.currentSong
      }
      
      // Find first song where listenCount is 0, null, or undefined
      const index = this.queue.findIndex(song => {
        const listenCount = song.listenCount ?? song.userSong?.listenCount
        return !listenCount || listenCount === 0
      })
      
      if (index >= 0) {
        this.currentIndex = index
        this.currentSong = this.queue[index]
        return this.queue[index]
      }
      
      // All songs have been played - don't select anything
      return null
    },

    setAudioElement(element) {
      this.audioElement = element
      if (element) {
        element.volume = this.volume
        element.addEventListener('timeupdate', () => {
          this.currentTime = element.currentTime * 1000
        })
        element.addEventListener('loadedmetadata', () => {
          this.duration = element.duration * 1000
        })
        element.addEventListener('ended', () => {
          this.handleSongEnd()
        })
      }
    },

    async play(song) {
      // If same song is already selected and audio is loaded, just resume
      if (this.currentSong?.id === song.id && this.audioElement?.src) {
        this.resume()
        return
      }

      // If there's a current song playing and we're switching to a different song,
      // mark the current song as skipped (unless it's ending naturally or we're navigating)
      // Note: Skip is handled in next()/previous() when navigating, so we don't duplicate here
      if (this.currentSong?.id && this.currentSong.id !== song.id && !this.isSongEndingNaturally && !this.isNavigating) {
        // Mark as skipped asynchronously (don't block playback)
        this.markCurrentSongAsSkipped()
      }

      // Clean up previous object URL to prevent memory leaks
      if (this.currentObjectUrl) {
        URL.revokeObjectURL(this.currentObjectUrl)
        this.currentObjectUrl = null
      }

      if (!this.audioElement || !song.id) {
        this.isNavigating = false
        return
      }

      // Reset flags when starting a new song
      this.isSongEndingNaturally = false
      this.isNavigating = false

      try {
        // Fetch audio blob using authenticated request
        const audioBlob = await songsAPI.getSongAudioBlob(song.id)
        
        // Create object URL from blob
        const objectUrl = URL.createObjectURL(audioBlob)
        this.currentObjectUrl = objectUrl
        this.currentSong = song

        // Fetch liked status for the new song
        this.fetchLikedStatus(song.id)

        // Set audio element source to object URL
        this.audioElement.src = objectUrl
        this.audioElement.load()
        
        // Play the audio
        await this.audioElement.play()
        this.isPlaying = true
      } catch (error) {
        console.error('Error playing song:', error)
        // Show user-friendly error message (non-blocking)
        if (error.message) {
          console.warn(`Failed to play song: ${error.message}`)
        } else {
          console.warn('Failed to play song. Please try again.')
        }
        
        // Check if song is not working and remove it from queue
        const { isSongNotWorking } = await import('@/utils/songHelpers')
        if (song && isSongNotWorking(song, error)) {
          console.warn(`Song ${song.id} appears to be not working, removing from queue`)
          try {
            const { useQueueStore } = await import('./queue')
            const queueStore = useQueueStore()
            await queueStore.removeSongFromQueue(song.id)
          } catch (removeError) {
            console.error('Failed to remove non-working song from queue:', removeError)
          }
        }
        
        this.currentSong = null
        this.isPlaying = false
        
        // Automatically try next song on failure
        if (this.queue.length > 0) {
          this.next()
        }
      }
    },

    pause() {
      if (this.audioElement) {
        this.audioElement.pause()
        this.isPlaying = false
      }
    },

    resume() {
      if (!this.audioElement) {
        // If no audio element and no song selected, try to select first unplayed song
        if (!this.currentSong) {
          const unplayedSong = this.selectFirstUnplayedSong()
          if (unplayedSong) {
            // Play the selected unplayed song
            this.play(unplayedSong)
            return
          }
        }
        // Can't resume without audio element
        return
      }
      
      // If currentSong is null but audio element exists, try to select first unplayed song
      if (!this.currentSong && this.queue.length > 0) {
        const unplayedSong = this.selectFirstUnplayedSong()
        if (unplayedSong) {
          // Play the selected unplayed song
          this.play(unplayedSong)
          return
        }
      }
      
      // Resume playback if we have both audio element and current song
      if (this.currentSong && this.audioElement) {
        this.audioElement.play().then(() => {
          this.isPlaying = true
        }).catch((error) => {
          console.error('Error resuming song:', error)
        })
      }
    },

    togglePlay() {
      if (this.isPlaying) {
        this.pause()
      } else {
        // If no song is selected, try to select and play first unplayed song
        if (!this.currentSong) {
          const unplayedSong = this.selectFirstUnplayedSong()
          if (unplayedSong) {
            this.play(unplayedSong)
            return
          }
          // No unplayed song found, can't play
          return
        }
        this.resume()
      }
    },

    seekTo(time) {
      if (this.audioElement) {
        this.audioElement.currentTime = time / 1000
        this.currentTime = time
      }
    },

    setVolume(volume) {
      this.volume = volume
      if (this.audioElement) {
        this.audioElement.volume = volume
      }
    },

    next() {
      // If repeat is 'one', just restart the same song (don't mark as skipped)
      if (this.repeat === 'one' && this.currentSong) {
        this.isNavigating = true
        this.play(this.currentSong)
        return
      }
      
      if (this.queue.length === 0) {
        return
      }
      
      // Set navigation flag to prevent duplicate skip calls in play()
      this.isNavigating = true
      
      // Mark current song as skipped before moving to next (if not ending naturally)
      if (this.currentSong?.id && !this.isSongEndingNaturally) {
        this.markCurrentSongAsSkipped()
      }
      
      if (this.hasNext) {
        const nextIndex = this.currentIndex + 1
        if (nextIndex < this.queue.length) {
          this.currentIndex = nextIndex
          this.play(this.queue[nextIndex])
        }
      } else {
        // Queue finished - automatically loop back to beginning
        // This happens regardless of repeat mode setting
        this.currentIndex = 0
        if (this.queue.length > 0) {
          this.play(this.queue[0])
        }
      }
    },

    previous() {
      // If repeat is 'one', just restart the same song (don't mark as skipped)
      if (this.repeat === 'one' && this.currentSong) {
        this.isNavigating = true
        this.play(this.currentSong)
        return
      }
      
      // Set navigation flag to prevent duplicate skip calls in play()
      this.isNavigating = true
      
      // Mark current song as skipped before moving to previous (if not ending naturally)
      if (this.currentSong?.id && !this.isSongEndingNaturally) {
        this.markCurrentSongAsSkipped()
      }
      
      if (this.hasPrevious) {
        const prevIndex = this.currentIndex - 1
        if (prevIndex >= 0) {
          this.currentIndex = prevIndex
          this.play(this.queue[prevIndex])
        }
      } else if (this.repeat === 'all' && this.queue.length > 0) {
        this.currentIndex = this.queue.length - 1
        this.play(this.queue[this.queue.length - 1])
      }
    },

    toggleShuffle() {
      this.shuffle = !this.shuffle
      if (this.shuffle && this.queue.length > 1) {
        // Shuffle queue
        const shuffled = [...this.queue]
        for (let i = shuffled.length - 1; i > 0; i--) {
          const j = Math.floor(Math.random() * (i + 1))
          ;[shuffled[i], shuffled[j]] = [shuffled[j], shuffled[i]]
        }
        this.queue = shuffled
        this.currentIndex = this.queue.findIndex(s => s.id === this.currentSong?.id)
      }
    },

    toggleRepeat() {
      if (this.repeat === false) {
        this.repeat = 'all'
      } else if (this.repeat === 'all') {
        this.repeat = 'one'
      } else {
        this.repeat = false
      }
    },

    mergeQueue(newSongs) {
      if (!Array.isArray(newSongs) || newSongs.length === 0) {
        return
      }

      // Get IDs of songs already in queue
      const existingIds = new Set(this.queue.map(s => s.id))
      
      // Append only new songs that aren't already in queue
      // Maintain play order (new songs added to end)
      const songsToAdd = newSongs.filter(song => !existingIds.has(song.id))
      
      if (songsToAdd.length > 0) {
        this.queue = [...this.queue, ...songsToAdd]
      }
    },

    setQueue(songs, startIndex = 0, shouldMerge = false) {
      if (shouldMerge) {
        // Merge with existing queue instead of replacing
        this.mergeQueue(songs)
        // Don't restart playback unless explicitly requested
        // Update currentIndex if the song at currentIndex was removed
        if (this.currentIndex >= 0 && this.currentIndex < this.queue.length) {
          // Current song still exists, keep playing
          return
        }
        // If currentIndex is invalid, try to find current song or start from beginning
        if (this.currentSong) {
          const foundIndex = this.queue.findIndex(s => s.id === this.currentSong.id)
          if (foundIndex >= 0) {
            this.currentIndex = foundIndex
            return
          }
        }
        // If we get here, current song is not in queue, but don't auto-play
        // Let the user decide what to do
      } else {
        // Replace queue (original behavior)
        if (!Array.isArray(songs) || songs.length === 0) {
          return
        }
        this.queue = songs
        // Ensure startIndex is valid
        const validIndex = Math.max(0, Math.min(startIndex, songs.length - 1))
        this.currentIndex = validIndex
        const songToPlay = songs[validIndex]
        if (songToPlay && songToPlay.id) {
          this.play(songToPlay)
        }
      }
    },

    addToQueue(song) {
      this.queue.push(song)
    },

    removeFromQueue(index) {
      if (index === this.currentIndex) {
        // If removing current song, play next
        this.queue.splice(index, 1)
        if (this.queue.length > 0) {
          this.currentIndex = Math.min(index, this.queue.length - 1)
          this.play(this.queue[this.currentIndex])
        } else {
          this.currentSong = null
          this.isPlaying = false
          this.currentIndex = -1
        }
      } else {
        this.queue.splice(index, 1)
        if (index < this.currentIndex) {
          this.currentIndex--
        }
      }
    },

    handleSongEnd() {
      // Mark that the song is ending naturally (not being skipped)
      this.isSongEndingNaturally = true
      
      // Mark current song as finished
      if (this.currentSong?.id) {
        this.markCurrentSongAsFinished()
      }
      
      if (this.repeat === 'one') {
        // When repeating, don't navigate, just restart
        this.isNavigating = true
        this.play(this.currentSong)
        this.isSongEndingNaturally = false
      } else {
        // Call next() which will handle navigation
        // Reset flag after next() completes (it will call play() which resets it)
        this.next()
        // Note: isSongEndingNaturally will be reset in next() -> play()
      }
    },

    clear() {
      // Clean up object URL
      if (this.currentObjectUrl) {
        URL.revokeObjectURL(this.currentObjectUrl)
        this.currentObjectUrl = null
      }

      this.currentSong = null
      this.isPlaying = false
      this.currentTime = 0
      this.duration = 0
      this.queue = []
      this.currentIndex = -1
      this.isLiked = false
      if (this.audioElement) {
        this.audioElement.pause()
        this.audioElement.src = ''
      }
    },
  },
})


