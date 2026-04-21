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

  // v0.4.2 PR D — bulk Soulseek availability probe. Non-downloading;
  // the UI uses the result only to color rows and let the user decide
  // whether to hit Queue. Probe results are NOT a guarantee the
  // eventual ladder will pass the quality gate — just "any peer at all?"
  //
  // items: [{title, artist, catalogNumber?}, ...]
  // returns: [{available, peerCount}, ...] parallel to input
  //
  // 5 s typical wall time for ~20 items; capped at 100 items per
  // request by the backend (400 beyond that).
  async checkAvailability(items) {
    if (!items || items.length === 0) return []
    try {
      const response = await client.post(
        `${API_URLS.QUEUE}/search/availability`,
        { items },
      )
      return response.data?.results || []
    } catch (error) {
      // Availability is a non-critical enhancement; failure should
      // degrade gracefully — treat as "unknown" (neither available
      // nor not_found) so all rows just stay neutral.
      console.warn('availability check failed:', error.message)
      return []
    }
  },
}
