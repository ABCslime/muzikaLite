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

  // v0.4.4: expand a Discogs album into individual tracks and add
  // each to the playlist. The backend fetches the tracklist, runs
  // the search-acquire flow per track, and returns a {added, total}
  // summary so the UI can toast progress. Tracks that probe
  // not_found stay in the playlist; AlbumView re-probes them on
  // mount.
  async addAlbumToPlaylist(playlistId, releaseId) {
    const response = await client.post(
      `${API_URLS.PLAYLIST}/${playlistId}/album`,
      { releaseId },
    )
    return response.data
  },
}

