import { createRouter, createWebHistory } from "vue-router";

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: "/",
      name: "prototype",
      component: () => import("@/views/PrototypeView.vue"),
    },
  ],
});

export default router;
