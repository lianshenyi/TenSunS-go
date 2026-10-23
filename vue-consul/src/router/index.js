import Vue from 'vue'
import Router from 'vue-router'

Vue.use(Router)

/* Layout */
import Layout from '@/layout'

/**
 * constantRoutes
 * a base page that does not have permission requirements
 * all roles can be accessed
 */
export const constantRoutes = [
  {
    path: '/login',
    component: () => import('@/views/login/index'),
    hidden: true
  },

  {
    path: '/404',
    component: () => import('@/views/404'),
    hidden: true
  },

  {
    path: '/',
    component: Layout,
    redirect: '/nodes/jobs',
    hidden: true
  },

  {
    path: '/nodes',
    component: Layout,
    redirect: '/nodes/jobs',
    name: '云资源管理',
    meta: { title: '云资源管理', icon: 'el-icon-shopping-bag-2' },
    children: [
      {
        path: 'jobs',
        name: '接入云厂商',
        component: () => import('@/views/node-exporter/jobs'),
        meta: { title: '接入云厂商', icon: 'el-icon-school' }
      },
      {
        path: 'ecs',
        name: 'ECS',
        component: () => import('@/views/node-exporter/index'),
        redirect: '/nodes/ecs/lists',
        meta: { title: 'ECS管理', icon: 'el-icon-cpu' },
        children: [
          {
            path: 'jms',
            name: 'JumpServer',
            component: () => import('@/views/jms/index'),
            meta: { title: 'JumpServer 同步', icon: 'el-icon-copy-document' }
          },
          {
            path: 'lists',
            name: '云主机列表',
            component: () => import('@/views/node-exporter/lists'),
            meta: { title: '云主机列表', icon: 'el-icon-cloudy' }
          }
        ]
      },
      {
        path: 'rds',
        name: 'RDS',
        component: () => import('@/views/rds/index'),
        redirect: '/nodes/rds/lists',
        meta: { title: 'MySQL管理', icon: 'el-icon-coin' },
        children: [
          {
            path: 'lists',
            name: '云MySQL列表',
            component: () => import('@/views/rds/lists'),
            meta: { title: '云MySQL列表', icon: 'el-icon-cloudy' }
          }
        ]
      },
      {
        path: 'redis',
        name: 'REDIS',
        component: () => import('@/views/redis/index'),
        redirect: '/nodes/redis/lists',
        meta: { title: 'REDIS管理', icon: 'el-icon-guide' },
        children: [
          {
            path: 'lists',
            name: '云REDIS列表',
            component: () => import('@/views/redis/lists'),
            meta: { title: '云REDIS列表', icon: 'el-icon-cloudy' }
          }
        ]
      }
    ]
  },

  // 404 page must be placed at the end !!!
  { path: '*', redirect: '/404', hidden: true }
]

const createRouter = () => new Router({
  // mode: 'history', // require service support
  scrollBehavior: () => ({ y: 0 }),
  routes: constantRoutes
})

const router = createRouter()

// Detail see: https://github.com/vuejs/vue-router/issues/1234#issuecomment-357941465
export function resetRouter() {
  const newRouter = createRouter()
  router.matcher = newRouter.matcher // reset router
}

export default router
