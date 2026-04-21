// Use proxy paths in development to avoid CORS issues
// In production, these should be set via environment variables
export const API_URLS = {
  AUTH: import.meta.env.VITE_AUTH_API_URL || '/api/auth',
  PLAYLIST: import.meta.env.VITE_PLAYLIST_API_URL || '/api/playlist',
  QUEUE: import.meta.env.VITE_QUEUE_API_URL || '/api/queue',
  USER: import.meta.env.VITE_USER_API_URL || '/api/user',
  DISCOGS: import.meta.env.VITE_DISCOGS_API_URL || '/api/discogs',
}

export const STORAGE_KEYS = {
  TOKEN: 'digeper_token',
  USER: 'digeper_user',
}

