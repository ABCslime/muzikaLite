import client from './client'
import { API_URLS } from '@/utils/constants'

// v0.4.2 PR C — Discogs browse endpoints.
//
// Read-only wrappers over GET /api/discogs/{artist,label,release}/:id.
// Returns the server's JSON verbatim. Errors bubble to the caller
// (each view handles 404 / 503 / 502 separately for clearer UX than
// a single generic failure).

export const discogsAPI = {
  async getArtist(id) {
    const response = await client.get(`${API_URLS.DISCOGS}/artist/${id}`)
    return response.data
  },

  async getLabel(id) {
    const response = await client.get(`${API_URLS.DISCOGS}/label/${id}`)
    return response.data
  },

  async getRelease(id) {
    const response = await client.get(`${API_URLS.DISCOGS}/release/${id}`)
    return response.data
  },
}
