<template>
  <!-- v0.4.4: SongItem-style row layout for Discogs releases the
       backend classified as Album (LP/Album/EP/Mini-Album). The
       single difference from ReleaseGrid is the action button:
       instead of "Queue" + Soulseek availability probe, this row
       carries an "Add to playlist" button that, when clicked,
       opens the existing PlaylistSelectionModal — but the modal
       routes to addAlbumToPlaylist (server-side expansion of the
       full tracklist) instead of addSongToPlaylist.

       Soulseek probe is intentionally NOT run on render — even a
       50-album discography would multiply our Soulseek query
       budget by 5x for data the user doesn't need until they
       actually click "Add". The probe happens per-track when
       the album is actually added. -->
  <div v-if="releases.length === 0" class="text-gray-600 py-6 text-center">
    No albums.
  </div>
  <ul v-else class="space-y-1">
    <li
      v-for="r in releases"
      :key="keyFor(r)"
      class="flex items-center gap-4 px-4 py-2 pixel-texture transition-colors bg-pinkish-white hover:bg-pinkish-white-hover"
    >
      <!-- Cover. Same overlay-on-gradient pattern as ReleaseGrid. -->
      <div
        class="relative w-12 h-12 flex-shrink-0 pixel-border bg-gradient-to-br from-vibrant-pink to-vibrant-purple border-vibrant-pink overflow-hidden flex items-center justify-center"
      >
        <svg class="w-6 h-6 opacity-70 text-white" fill="currentColor" viewBox="0 0 20 20">
          <path
            d="M18 3a1 1 0 00-1.196-.98l-10 2A1 1 0 006 5v9.114A4.369 4.369 0 005 14c-1.657 0-3 .895-3 2s1.343 2 3 2 3-.895 3-2V7.82l8-1.6v5.894A4.37 4.37 0 0015 12c-1.657 0-3 .895-3 2s1.343 2 3 2 3-.895 3-2V3z"
          />
        </svg>
        <img
          v-if="r.thumb"
          :src="r.thumb"
          :alt="r.title"
          class="absolute inset-0 w-full h-full object-cover"
          loading="lazy"
          referrerpolicy="no-referrer"
          @error="(e) => e.target.style.display='none'"
        />
      </div>

      <div class="flex-1 min-w-0">
        <p class="font-medium text-gray-900 truncate">{{ r.title }}</p>
        <p class="text-xs text-gray-600 truncate">
          <span>{{ r.artist }}</span>
          <span v-if="r.year"> · {{ r.year }}</span>
          <span v-if="r.catalogNumber"> · {{ r.catalogNumber }}</span>
        </p>
      </div>

      <button
        type="button"
        class="px-3 py-1 pixel-border text-xs font-semibold transition-colors bg-vibrant-purple-light border-vibrant-purple text-gray-900 hover:bg-vibrant-purple hover:text-white"
        title="Add full album to playlist"
        @click="emit('add-album', r)"
      >
        + Album
      </button>
    </li>
  </ul>
</template>

<script setup>
defineProps({
  releases: { type: Array, required: true },
})
const emit = defineEmits(['add-album'])

function keyFor(r) {
  return `${r.artist}|${r.title}|${r.catalogNumber}|${r.year}`
}
</script>
