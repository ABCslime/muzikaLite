import client from './client'
import { API_URLS } from '@/utils/constants'

export const queueAPI = {
  async getQueue() {
    const url = `${API_URLS.QUEUE}/queue`
    console.group('🔵 Queue API - GET Request')
    console.log('📡 Request URL:', url)
    console.log('🔗 Base URL:', API_URLS.QUEUE)
    console.log('⏰ Timestamp:', new Date().toISOString())
    
    try {
      console.log('📤 Sending GET request...')
      const response = await client.get(url)
      
      console.log('✅ Response received:')
      console.log('   Status:', response.status)
      console.log('   Status Text:', response.statusText)
      console.log('   Headers:', response.headers)
      console.log('   Data:', response.data)
      console.log('   Data Type:', typeof response.data)
      console.log('   Data Keys:', response.data ? Object.keys(response.data) : 'null')
      
      if (response.data?.songs) {
        console.log(`   Songs Count: ${response.data.songs.length}`)
        if (response.data.songs.length > 0) {
          console.log('   First Song:', response.data.songs[0])
        }
      }
      
      console.groupEnd()
      return response.data
    } catch (error) {
      console.error('❌ Queue GET Request Failed:')
      console.error('   Error Type:', error.constructor.name)
      console.error('   Error Message:', error.message)
      
      if (error.response) {
        console.error('   Response Status:', error.response.status)
        console.error('   Response Status Text:', error.response.statusText)
        console.error('   Response Headers:', error.response.headers)
        console.error('   Response Data:', error.response.data)
        console.error('   Request URL:', error.config?.url)
        console.error('   Request Method:', error.config?.method)
        console.error('   Request Headers:', error.config?.headers)
      } else if (error.request) {
        console.error('   No response received')
        console.error('   Request:', error.request)
        console.error('   This usually means the server is not reachable')
      } else {
        console.error('   Error setting up request:', error.message)
      }
      
      console.error('   Full Error Object:', error)
      console.groupEnd()
      throw error
    }
  },

  async addSongToQueue(songId, position) {
    try {
      await client.post(`${API_URLS.QUEUE}/queue`, {
        songId,
        position,
      })
    } catch (error) {
      console.error('Error adding song to queue:', error)
      throw error
    }
  },

  async markSongAsSkipped(songId) {
    try {
      const response = await client.post(`${API_URLS.QUEUE}/queue/skipped`, {
        songId,
      })
      return { success: true, data: response.data }
    } catch (error) {
      if (error.response) {
        const status = error.response.status
        if (status === 401) {
          console.error('Unauthorized - please login again')
        } else if (status === 400) {
          const errorMessage = error.response.data?.message || error.response.data || 'Bad request'
          console.error(`Bad request: ${errorMessage}`)
        } else {
          console.error(`Server error: ${status}`)
        }
      } else {
        console.error('Error marking song as skipped:', error.message)
      }
      throw error
    }
  },

  async markSongAsFinished(songId) {
    try {
      const response = await client.post(`${API_URLS.QUEUE}/queue/finished`, {
        songId,
      })
      return { success: true, data: response.data }
    } catch (error) {
      if (error.response) {
        const status = error.response.status
        if (status === 401) {
          console.error('Unauthorized - please login again')
        } else if (status === 400) {
          const errorMessage = error.response.data?.message || error.response.data || 'Bad request'
          console.error(`Bad request: ${errorMessage}`)
        } else {
          console.error(`Server error: ${status}`)
        }
      } else {
        console.error('Error marking song as finished:', error.message)
      }
      throw error
    }
  },

  async removeSongFromQueue(songId) {
    try {
      const response = await client.delete(`${API_URLS.QUEUE}/queue/${songId}`)
      return { success: true, data: response.data }
    } catch (error) {
      if (error.response) {
        const status = error.response.status
        if (status === 401) {
          console.error('Unauthorized - please login again')
        } else if (status === 400) {
          const errorMessage = error.response.data?.message || error.response.data || 'Bad request'
          console.error(`Bad request: ${errorMessage}`)
        } else if (status === 404) {
          console.error(`Song ${songId} not found in queue`)
        } else {
          console.error(`Server error: ${status}`)
        }
      } else {
        console.error('Error removing song from queue:', error.message)
      }
      throw error
    }
  },

  // v0.4 PR 3: user-initiated search. POSTs the raw query; the backend
  // normalizes (lowercase + strip punctuation + collapse whitespace). A
  // stub is created synchronously; the queue entry appears asynchronously
  // as the Discogs seeder + download ladder complete.
  //
  // Returns { songId, query } — songId is the stub's UUID, query is the
  // normalized form actually used. Throws on 400 (empty normalized
  // query) or network errors.
  async searchAndQueue(query) {
    try {
      const response = await client.post(`${API_URLS.QUEUE}/search`, { query })
      return response.data
    } catch (error) {
      if (error.response) {
        const status = error.response.status
        if (status === 400) {
          const msg = error.response.data?.message || 'query is empty after normalization'
          console.error(`Bad search: ${msg}`)
        } else if (status === 401) {
          console.error('Unauthorized - please login again')
        } else {
          console.error(`Search server error: ${status}`)
        }
      } else {
        console.error('Error searching:', error.message)
      }
      throw error
    }
  },
}

