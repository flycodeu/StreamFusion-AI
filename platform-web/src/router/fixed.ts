import type { RouteRecordRaw } from 'vue-router'

/** Application pages are explicit and lazy; backend menus only discover views/. */
export const fixedRoutes: readonly RouteRecordRaw[] = [
  { path: '/', redirect: '/home' },
  { path: '/login', name: 'login', component: () => import('../pages/auth/LoginView.vue') },
  {
    path: '/home',
    name: 'home',
    component: () => import('../pages/home/HomeView.vue'),
    meta: { title: '首页' },
  },
  {
    path: '/profile',
    name: 'profile',
    component: () => import('../pages/account/ProfileView.vue'),
    meta: { title: '个人信息' },
  },
  {
    path: '/change-password',
    name: 'change-password',
    component: () => import('../pages/auth/ChangePasswordView.vue'),
    meta: { title: '修改密码' },
  },
  {
    path: '/forbidden',
    name: 'forbidden',
    component: () => import('../pages/error/ForbiddenView.vue'),
    meta: { title: '访问受限' },
  },
  {
    path: '/unavailable',
    name: 'unavailable',
    component: () => import('../pages/error/UnavailableView.vue'),
    meta: { title: '服务暂不可用' },
  },
]

export const fixedPaths: ReadonlySet<string> = new Set(fixedRoutes.map((route) => route.path))
