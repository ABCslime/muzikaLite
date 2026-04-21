import { defineStore } from 'pinia'
import { preferencesAPI } from '@/api/preferences'

// v0.4.1 PR A — local cache of the user's genre preferences.
//
// Fetched once on SettingsView mount; saved by SettingsView's save button.
// The refiller on the server is the ultimate source of truth — this store
// just mirrors what we've persisted most recently for UI responsiveness.
export const usePreferencesStore = defineStore('preferences', {
  state: () => ({
    bandcampTags: [],
    discogsGenres: [],
    loading: false,
    error: null,
  }),

  actions: {
    async fetch() {
      this.loading = true
      this.error = null
      try {
        const p = await preferencesAPI.get()
        this.bandcampTags = p.bandcampTags || []
        this.discogsGenres = p.discogsGenres || []
        return { success: true }
      } catch (error) {
        this.error = error.response?.data?.message || error.message || 'Failed to load preferences'
        return { success: false, error: this.error }
      } finally {
        this.loading = false
      }
    },

    async save({ bandcampTags, discogsGenres }) {
      this.loading = true
      this.error = null
      try {
        const p = await preferencesAPI.replace({ bandcampTags, discogsGenres })
        // Server returns the normalized form — trust that.
        this.bandcampTags = p.bandcampTags || []
        this.discogsGenres = p.discogsGenres || []
        return { success: true, data: p }
      } catch (error) {
        this.error = error.response?.data?.message || error.message || 'Failed to save preferences'
        return { success: false, error: this.error }
      } finally {
        this.loading = false
      }
    },
  },
})
