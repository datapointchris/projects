<template>
  <div class="main">
    <ProjectsView />
  </div>
  <NotificationToast />
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import NotificationToast from '@/components/NotificationToast.vue'
import ProjectsView from '@/views/ProjectsView.vue'

onMounted(() => {
  // index.html paints the body dark before the styles load, and the theme's colors show once it lets go.
  document.body.style.background = ''
})
</script>

<style lang="scss" scoped>
@use 'abstracts/breakpoints';

.main {
  min-height: 100vh;
  padding: var(--space-m);
  padding-top: calc(var(--space-m) + env(safe-area-inset-top));
  padding-bottom: calc(var(--space-m) + env(safe-area-inset-bottom));

  @include breakpoints.phone {
    padding-top: var(--space-s);
    padding-left: calc(var(--space-xs) + env(safe-area-inset-left));
    padding-right: calc(var(--space-xs) + env(safe-area-inset-right));
  }
}
</style>
