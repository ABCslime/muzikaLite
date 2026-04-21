<template>
  <div class="flex h-screen">
    <Sidebar />
    <div class="flex-1 flex flex-col overflow-hidden">
      <TopBar />
      <div class="flex-1 overflow-y-auto pb-24">
        <div class="p-8">
          <!-- v0.4.2 PR A: pinned genres surface here as toggle chips.
               Clicking a chip unpins it (removes from preferences;
               refiller stops seeding from it). Adding new genres still
               happens in /settings. Bandcamp tags + Discogs genres are
               distinct sets — they get two separate chip rows so users
               can reason about which source each belongs to. -->
          <div class="flex items-center justify-between flex-wrap gap-4 mb-8">
            <h1 class="text-3xl font-bold text-gray-900">Home</h1>
            <div class="flex items-center gap-2 flex-wrap">
              <GenreChip
                v-for="t in prefsStore.bandcampTags"
                :key="`bc-${t}`"
                :label="t"
                source="bandcamp"
                @unpin="unpinBandcamp(t)"
              />
              <GenreChip
                v-for="g in prefsStore.discogsGenres"
                :key="`dg-${g}`"
                :label="g"
                source="discogs"
                @unpin="unpinDiscogs(g)"
              />
              <router-link
                v-if="totalPinned === 0"
                to="/settings"
                class="text-sm text-gray-600 hover:text-vibrant-pink underline"
              >
                Pin genres in Settings →
              </router-link>
              <router-link
                v-else
                to="/settings"
                class="text-xs text-gray-500 hover:text-vibrant-pink"
                title="Add more genres"
              >
                +
              </router-link>
            </div>
          </div>

          <div class="mb-8">
            <h2 class="text-2xl font-bold text-gray-900 mb-4">Queue</h2>
            <QueueView />
          </div>
        </div>
      </div>
      <PlayerBar />
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted } from 'vue'
import Sidebar from '@/components/layout/Sidebar.vue'
import TopBar from '@/components/layout/TopBar.vue'
import PlayerBar from '@/components/layout/PlayerBar.vue'
import QueueView from '@/components/queue/QueueView.vue'
import GenreChip from '@/components/home/GenreChip.vue'
import { usePreferencesStore } from '@/stores/preferences'

const prefsStore = usePreferencesStore()

const totalPinned = computed(
  () => prefsStore.bandcampTags.length + prefsStore.discogsGenres.length,
)

onMounted(() => {
  // Idempotent; SettingsView also fetches.
  prefsStore.fetch()
})

async function unpinBandcamp(tag) {
  const next = prefsStore.bandcampTags.filter(t => t !== tag)
  await prefsStore.save({
    bandcampTags: next,
    discogsGenres: prefsStore.discogsGenres,
  })
}

async function unpinDiscogs(genre) {
  const next = prefsStore.discogsGenres.filter(g => g !== genre)
  await prefsStore.save({
    bandcampTags: prefsStore.bandcampTags,
    discogsGenres: next,
  })
}
</script>
