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

  // v0.4.2 PR E — artist-broad availability. One Soulseek search per
  // artist, then the backend filters the returned filenames against
  // the title list with the shared filematch package. Much more
  // efficient and reliable for artist + album pages than the N
  // per-release probes of PR D, because:
  //
  //   - Soulseek scales better with fewer deeper searches than many
  //     shallow ones (peer response is rate-limited per search).
  //   - Filename filtering tolerates title variance ("Song (Remastered)"
  //     vs "Song"), parens, punctuation, and stopwords — matching the
  //     same token-set semantics the download worker now uses.
  //
  // artist: string, titles: string[] — returns results[] parallel to titles.
  async checkAvailabilityByArtist(artist, titles) {
    if (!artist || !titles || titles.length === 0) return []
    try {
      const response = await client.post(
        `${API_URLS.QUEUE}/search/availability/by-artist`,
        { artist, titles },
      )
      return response.data?.results || []
    } catch (error) {
      console.warn('artist-broad availability check failed:', error.message)
      return []
    }
  },
}
