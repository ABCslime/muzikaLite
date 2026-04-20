/**
 * Default placeholder image for songs (SVG data URL)
 * A simple music note icon on dark gray background
 */
const DEFAULT_SONG_IMAGE = "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'%3E%3Crect fill='%23282828' width='100' height='100'/%3E%3Cpath fill='%239B9B9B' d='M35 25c-2.5 0-4.5 2-4.5 4.5v35c-2-1.5-4.5-2.5-7.5-2.5-6.5 0-11.5 5-11.5 11.5S18 85.5 24.5 85.5s11.5-5 11.5-11.5V45l30-8v25c-2-1.5-4.5-2.5-7.5-2.5-6.5 0-11.5 5-11.5 11.5S43 71.5 49.5 71.5s11.5-5 11.5-11.5V25H35z'/%3E%3C/svg%3E"

/**
 * Get song title with fallback to "Unknown"
 * @param {Object} song - Song object
 * @returns {string} Song title or "Unknown"
 */
export function getSongTitle(song) {
  if (!song) return 'Unknown'
  const title = song.title
  if (!title || title.trim() === '') return 'Unknown'
  return title
}

/**
 * Get song artist with fallback to "Unknown Artist"
 * @param {Object} song - Song object
 * @returns {string} Song artist or "Unknown Artist"
 */
export function getSongArtist(song) {
  if (!song) return 'Unknown Artist'
  const artist = song.artist
  if (!artist || artist.trim() === '') return 'Unknown Artist'
  return artist
}

/**
 * Get song image URL with fallback to default placeholder
 * @param {Object} song - Song object
 * @returns {string} Song image URL or default placeholder
 */
export function getSongImage(song) {
  if (!song) return DEFAULT_SONG_IMAGE
  const image = song.image || song.albumArt || song.cover
  if (!image || image.trim() === '') return DEFAULT_SONG_IMAGE
  return image
}

/**
 * Check if a song is already in the queue
 * @param {Object} song - Song object with an id property
 * @param {Array} queueSongs - Array of songs in the queue
 * @returns {boolean} True if song is already in queue, false otherwise
 */
export function isSongInQueue(song, queueSongs) {
  if (!song || !song.id || !Array.isArray(queueSongs)) return false
  return queueSongs.some(queueSong => queueSong.id === song.id)
}

/**
 * Check if a song is not working based on error conditions
 * @param {Object} song - Song object
 * @param {Error} error - Error object from playback attempt
 * @returns {boolean} True if song appears to be not working, false otherwise
 */
export function isSongNotWorking(song, error) {
  if (!song || !error) return false
  
  // Check for common error conditions that indicate a song is not working
  const errorMessage = error.message?.toLowerCase() || ''
  const errorStatus = error.response?.status
  
  // Network errors, 404, 403, 500, etc. indicate the song might not be available
  if (errorStatus === 404 || errorStatus === 403 || errorStatus === 500) {
    return true
  }
  
  // Check for specific error messages
  const notWorkingIndicators = [
    'not found',
    'forbidden',
    'unauthorized',
    'server error',
    'failed to load',
    'network error',
    'timeout',
    'corrupted',
    'invalid format'
  ]
  
  return notWorkingIndicators.some(indicator => errorMessage.includes(indicator))
}

