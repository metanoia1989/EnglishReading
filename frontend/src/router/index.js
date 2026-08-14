import { createRouter, createWebHistory } from 'vue-router'
import { auth } from '../store/auth'
import HomeView from '../views/HomeView.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'home', component: HomeView },
    { path: '/dataset/:id', name: 'dataset', component: () => import('../views/DatasetView.vue') },
    { path: '/read/:id', name: 'reader', component: () => import('../views/ReaderView.vue'), meta: { bare: true } },
    { path: '/login', name: 'login', component: () => import('../views/LoginView.vue') },
    { path: '/register', name: 'register', component: () => import('../views/RegisterView.vue') },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
  scrollBehavior() {
    return { top: 0 }
  },
})

router.beforeEach(async (to) => {
  await auth.init()
  return true
})

export default router
