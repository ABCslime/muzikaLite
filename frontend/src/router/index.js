import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/login',
      name: 'Login',
      component: () => import('@/views/LoginView.vue'),
      meta: { requiresAuth: false },
    },
    {
      path: '/',
      name: 'Home',
      component: () => import('@/views/HomeView.vue'),
      meta: { requiresAuth: true },
    },
    {
      path: '/playlists',
      name: 'Playlists',
      component: () => import('@/views/PlaylistsView.vue'),
      meta: { requiresAuth: true },
    },
    {
      path: '/playlist/:id',
      name: 'PlaylistDetail',
      component: () => import('@/views/PlaylistDetailView.vue'),
      meta: { requiresAuth: true },
    },
    // v0.4.1 PR A — genre preferences
    {
      path: '/settings',
      name: 'Settings',
      component: () => import('@/views/SettingsView.vue'),
      meta: { requiresAuth: true },
    },
    // v0.4.2 PR C — Discogs detail views. id is always a positive integer.
    {
      path: '/artist/:id',
      name: 'Artist',
      component: () => import('@/views/ArtistView.vue'),
      meta: { requiresAuth: true },
    },
    {
      path: '/label/:id',
      name: 'Label',
      component: () => import('@/views/LabelView.vue'),
      meta: { requiresAuth: true },
    },
    {
      path: '/album/:id',
      name: 'Album',
      component: () => import('@/views/AlbumView.vue'),
      meta: { requiresAuth: true },
    },
  ],
})

router.beforeEach((to, from, next) => {
  const authStore = useAuthStore()
  const requiresAuth = to.meta.requiresAuth !== false

  if (requiresAuth && !authStore.isAuthenticated) {
    next({ name: 'Login', query: { redirect: to.fullPath } })
  } else if (!requiresAuth && authStore.isAuthenticated && to.name === 'Login') {
    next({ name: 'Home' })
  } else {
    next()
  }
})

export default router

