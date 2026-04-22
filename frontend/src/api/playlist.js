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

  // v0.4.4: AlbumView on-mount reprobe. For each track on the
  // release, the backend looks up the caller's queue_entry and — if
  // status='not_found' — flips it back to 'probing' and republishes
  // RequestDownload. No-op for tracks the user hasn't added to any
  // playlist. Returns { reprobed, total }.
  async reprobeAlbum(releaseId) {
    // Route lives at /api/album/{releaseId}/reprobe, NOT under
    // /api/playlist/ — Go 1.22's mux conflict prevents nesting
    // /api/playlist/album/{…} alongside /api/playlist/{id}/song/{…}.
    const response = await client.post(
      `/api/album/${releaseId}/reprobe`,
    )
    return response.data
  },
}

