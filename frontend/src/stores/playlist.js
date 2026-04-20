import { defineStore } from 'pinia'
import { playlistAPI } from '@/api/playlist'

export const usePlaylistStore = defineStore('playlist', {
  state: () => ({
    playlists: [],
    currentPlaylist: null,
    loading: false,
    error: null,
  }),

  getters: {
    playlistCount: (state) => state.playlists.length,
  },

  actions: {
    async fetchPlaylists() {
      this.loading = true
      this.error = null
      try {
        this.playlists = await playlistAPI.getAllPlaylists()
        return { success: true }
      } catch (error) {
        this.error = error.response?.data?.error || error.message || 'Failed to fetch playlists'
        return { success: false, error: this.error }
      } finally {
        this.loading = false
      }
    },

    async fetchPlaylist(playlistId) {
      this.loading = true
      this.error = null
      try {
        const response = await playlistAPI.getPlaylist(playlistId)
        console.log('Playlist API response:', response)
        console.log('Response structure:', {
          hasPlaylist: !!response?.playlist,
          hasSongs: !!response?.songs,
          playlistName: response?.playlist?.name,
          songsCount: response?.songs?.length
        })
        this.currentPlaylist = response
        if (!response || !response.playlist) {
          console.error('Invalid playlist response structure:', response)
          this.error = 'Invalid playlist data received from server'
          return { success: false, error: this.error }
        }
        return { success: true, data: this.currentPlaylist }
      } catch (error) {
        console.error('Error fetching playlist:', error)
        console.error('Error response:', error.response)
        this.error = error.response?.data?.error || error.response?.data?.message || error.message || 'Failed to fetch playlist'
        return { success: false, error: this.error }
      } finally {
        this.loading = false
      }
    },

    async createPlaylist(name, description = null) {
      this.loading = true
      this.error = null
      try {
        const newPlaylist = await playlistAPI.createPlaylist(name, description)
        this.playlists.push(newPlaylist)
        return { success: true, data: newPlaylist }
      } catch (error) {
        this.error = error.response?.data?.error || error.message || 'Failed to create playlist'
        return { success: false, error: this.error }
      } finally {
        this.loading = false
      }
    },

    async deletePlaylist(playlistId) {
      this.loading = true
      this.error = null
      try {
        await playlistAPI.deletePlaylist(playlistId)
        this.playlists = this.playlists.filter(p => p.id !== playlistId)
        if (this.currentPlaylist?.playlist?.id === playlistId) {
          this.currentPlaylist = null
        }
        return { success: true }
      } catch (error) {
        this.error = error.response?.data?.error || error.message || 'Failed to delete playlist'
        return { success: false, error: this.error }
      } finally {
        this.loading = false
      }
    },

    async addSongToPlaylist(playlistId, songId) {
      this.loading = true
      this.error = null
      try {
        await playlistAPI.addSongToPlaylist(playlistId, songId)
        // Refresh current playlist if it's the one being modified
        if (this.currentPlaylist?.playlist?.id === playlistId) {
          await this.fetchPlaylist(playlistId)
        }
        return { success: true }
      } catch (error) {
        this.error = error.response?.data?.error || error.message || 'Failed to add song to playlist'
        return { success: false, error: this.error }
      } finally {
        this.loading = false
      }
    },

    async removeSongFromPlaylist(playlistId, songId) {
      this.loading = true
      this.error = null
      try {
        await playlistAPI.removeSongFromPlaylist(playlistId, songId)
        // Refresh current playlist if it's the one being modified
        if (this.currentPlaylist?.playlist?.id === playlistId) {
          await this.fetchPlaylist(playlistId)
        }
        return { success: true }
      } catch (error) {
        this.error = error.response?.data?.error || error.message || 'Failed to remove song from playlist'
        return { success: false, error: this.error }
      } finally {
        this.loading = false
      }
    },

    clearCurrentPlaylist() {
      this.currentPlaylist = null
    },

    clearError() {
      this.error = null
    },
  },
})

