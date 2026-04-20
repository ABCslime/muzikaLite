<template>
  <div class="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 gap-4">
    <PlaylistCard
      v-for="playlist in playlists"
      :key="playlist.id"
      :playlist="playlist"
    />
  </div>
</template>

<script setup>
import PlaylistCard from './PlaylistCard.vue'

defineProps({
  playlists: {
    type: Array,
    default: () => [],
  },
})
</script>

