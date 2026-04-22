// v0.5 PR D — similarity registry + per-user bucket weight tuning.
//
// Separate API module from queue.js (similar-mode toggle lives
// there because the backend route is /api/queue/similar-mode, a
// v0.5 PR B decision) to keep the two concerns legible at the
// call site — queueAPI.getSimilarMode is about the active seed,
// similarityAPI.listBuckets is about the Settings UI.
import client from './client'

export const similarityAPI = {
  // Registry snapshot: one entry per registered Bucket with the
  // metadata the Settings UI renders (id, label, description,
  // defaultWeight). Empty array when Discogs is disabled or no
  // buckets are registered — UI renders an informational message
  // in that case rather than silent blankness.
  async listBuckets() {
    const response = await client.get('/api/similarity/buckets')
    return response.data
  },

  // User's currently tuned weights, as a sparse map keyed by
  // bucket id. Missing keys = "use that bucket's DefaultWeight."
  // Empty object ({}) = "haven't tuned anything yet."
  async getWeights() {
    const response = await client.get('/api/similarity/weights')
    return response.data
  },

  // Replace the user's weight map. Pass {} to reset to defaults.
  // Unknown ids (buckets that aren't currently registered) are
  // accepted without validation so a v0.6 plugin's weight
  // survives a plugin restart.
  async setWeights(weights) {
    const response = await client.put('/api/similarity/weights', weights || {})
    return response.data
  },
}
