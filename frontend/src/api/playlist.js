import client from './client'
import { API_URLS } from '@/utils/constants'

export const playlistAPI = {
  async getAllPlaylists() {
    const response = await client.get(`${API_URLS.PLAYLIST}`)
    return response.data
  },

  async getPlaylist(playlistId) {
    const response = await client.get(`${API_URLS.PLAYLIST}/${playlistId}`)
    return response.data
  },

  async createPlaylist(name, description = null) {
    const response = await client.post(`${API_URLS.PLAYLIST}`, {
      name,
      description,
    })
    return response.data
  },

  async deletePlaylist(playlistId) {
    await client.delete(`${API_URLS.PLAYLIST}/${playlistId}`)
  },

  async addSongToPlaylist(playlistId, songId) {
    await client.post(`${API_URLS.PLAYLIST}/${playlistId}/song/${songId}`)
  },

  async removeSongFromPlaylist(playlistId, songId) {
    await client.delete(`${API_URLS.PLAYLIST}/${playlistId}/song/${songId}`)
  },
}

