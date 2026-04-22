// Helpers for "click an artist name anywhere in the UI and land on
// the artist detail page" — without each row having to embed a Discogs
// artist id at write time. Songs in the queue store an artist STRING
// (no discogs id), so the click resolves the id on demand by hitting
// the same Discogs preview endpoint the search dropdown uses.
//
// goToArtistByName(router, name): looks up the first artist match for
// `name` and pushes /artist/{id}. No-ops on empty name, no result, or
// transport error — better to swallow than surface a "we couldn't find
// the artist you just clicked on" toast for what the user perceives as
// a navigation, not a search.
//
// goToAlbum(router, releaseId): trivial wrapper kept here so the album
// row click sites have a single import alongside the artist helper.

import { queueAPI } from '@/api/queue'

export async function goToArtistByName(router, name) {
  const q = (name || '').trim()
  if (!q) return
  try {
    const preview = await queueAPI.previewSearch(q)
    // previewSearch returns the raw Preview JSON: {artists, releases, ...}.
    // We want the FIRST artist whose name matches case-insensitively if
    // possible (Discogs sometimes returns related/featured artists ahead
    // of the exact match). Fall back to the first row when no exact hit
    // — better to navigate to a near-miss than to no-op.
    const artists = preview?.artists || []
    if (artists.length === 0) return
    const exact = artists.find(a =>
      (a?.name || '').toLowerCase() === q.toLowerCase(),
    )
    const target = exact || artists[0]
    if (!target?.id) return
    router.push({ name: 'Artist', params: { id: target.id } })
  } catch {
    // Silent — see header docstring.
  }
}

export function goToAlbum(router, releaseId) {
  if (!releaseId) return
  router.push({ name: 'Album', params: { id: releaseId } })
}
