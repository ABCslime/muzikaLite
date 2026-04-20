<template>
  <div class="flex h-screen">
    <Sidebar />
    <div class="flex-1 flex flex-col overflow-hidden">
      <TopBar />
      <div class="flex-1 overflow-y-auto pb-24">
        <div class="p-8">
          <h1 class="text-3xl font-bold text-gray-900 mb-8">Home</h1>

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
import Sidebar from '@/components/layout/Sidebar.vue'
import TopBar from '@/components/layout/TopBar.vue'
import PlayerBar from '@/components/layout/PlayerBar.vue'
import QueueView from '@/components/queue/QueueView.vue'
</script>

