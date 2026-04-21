import client from './client'
import { API_URLS } from '@/utils/constants'

// v0.4.1 PR A — per-user genre preferences.
//
// The backend stores Bandcamp tags (free-form; user types them) and
// Discogs genres (closed vocabulary; picked from a preset list) in two
// normalized tables. The response shape is {bandcampTags, discogsGenres}.
// Empty lists mean "no preference" — the refiller falls back to
// MUZIKA_BANDCAMP_DEFAULT_TAGS / MUZIKA_DISCOGS_DEFAULT_GENRES.

export const preferencesAPI = {
  async get() {
    try {
      const response = await client.get(`${API_URLS.USER}/preferences`)
      return response.data
    } catch (error) {
      console.error('Error loading preferences:', error)
      throw error
    }
  },

  async replace(preferences) {
    try {
      const response = await client.put(`${API_URLS.USER}/preferences`, preferences)
      return response.data
    } catch (error) {
      if (error.response) {
        const status = error.response.status
        if (status === 400) {
          console.error('Bad preferences:', error.response.data)
        } else if (status === 401) {
          console.error('Unauthorized - please login again')
        } else {
          console.error(`Preferences server error: ${status}`)
        }
      } else {
        console.error('Error saving preferences:', error.message)
      }
      throw error
    }
  },
}
