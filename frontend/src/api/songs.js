import client from './client'
import { API_URLS } from '@/utils/constants'

/**
 * Fetch song audio file as a blob
 * @param {string} songId - UUID of the song
 * @returns {Promise<Blob>} - Audio file blob
 */
export const songsAPI = {
  async getSongAudioBlob(songId) {
    try {
      const url = `${API_URLS.QUEUE}/songs/${songId}`
      
      const response = await client.get(url, {
        responseType: 'blob',
      })
      
      return response.data
    } catch (error) {
      console.error('Error fetching song audio:', error)
      
      if (error.response?.status === 404) {
        throw new Error('Song not found')
      } else if (error.response?.status === 401) {
        throw new Error('Unauthorized - please log in again')
      } else if (error.response?.status === 500) {
        throw new Error('Server error - please try again later')
      } else if (error.request) {
        throw new Error('Network error - please check your connection')
      } else {
        throw new Error('Failed to load song')
      }
    }
  },

  /**
   * Get liked status for a song
   * @param {string} songId - UUID of the song
   * @returns {Promise<{liked: boolean}>} - Liked status
   */
  async getSongLikedStatus(songId) {
    try {
      const url = `${API_URLS.QUEUE}/songs/${songId}/liked`
      const response = await client.get(url)
      return response.data
    } catch (error) {
      console.error('Error fetching song liked status:', error)
      
      if (error.response?.status === 404) {
        throw new Error('Song not found')
      } else if (error.response?.status === 401) {
        throw new Error('Unauthorized - please log in again')
      } else if (error.response?.status === 400) {
        throw new Error('Bad request - song not found')
      } else if (error.response?.status === 500) {
        throw new Error('Server error - please try again later')
      } else if (error.request) {
        throw new Error('Network error - please check your connection')
      } else {
        throw new Error('Failed to get liked status')
      }
    }
  },

  /**
   * Mark a song as liked
   * @param {string} songId - UUID of the song
   * @returns {Promise<void>}
   */
  async markSongAsLiked(songId) {
    try {
      const url = `${API_URLS.QUEUE}/songs/${songId}/liked`
      await client.post(url)
    } catch (error) {
      console.error('Error marking song as liked:', error)
      
      if (error.response?.status === 404) {
        throw new Error('Song not found')
      } else if (error.response?.status === 401) {
        throw new Error('Unauthorized - please log in again')
      } else if (error.response?.status === 400) {
        throw new Error('Bad request - song not found')
      } else if (error.response?.status === 500) {
        throw new Error('Server error - please try again later')
      } else if (error.request) {
        throw new Error('Network error - please check your connection')
      } else {
        throw new Error('Failed to mark song as liked')
      }
    }
  },

  /**
   * Mark a song as unliked
   * @param {string} songId - UUID of the song
   * @returns {Promise<void>}
   */
  async markSongAsUnliked(songId) {
    try {
      const url = `${API_URLS.QUEUE}/songs/${songId}/unliked`
      await client.post(url)
    } catch (error) {
      console.error('Error marking song as unliked:', error)
      
      if (error.response?.status === 404) {
        throw new Error('Song not found')
      } else if (error.response?.status === 401) {
        throw new Error('Unauthorized - please log in again')
      } else if (error.response?.status === 400) {
        throw new Error('Bad request - song not found')
      } else if (error.response?.status === 500) {
        throw new Error('Server error - please try again later')
      } else if (error.request) {
        throw new Error('Network error - please check your connection')
      } else {
        throw new Error('Failed to mark song as unliked')
      }
    }
  },
}

