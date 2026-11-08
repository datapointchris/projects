<template>
  <div class="main">
    <!-- Nothing reads the API once logged out, because a read would start a login. -->
    <p
      v-if="loggedOut"
      data-testid="logged-out"
      class="logged-out">
      You have logged out.
      <a :href="LOGIN_AGAIN_HREF">Log in again</a>
    </p>
    <ProjectsView
      v-else
      @logged-out="loggedOut = true" />
  </div>
  <NotificationToast />
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { LOGIN_AGAIN_HREF } from '@/api/client'
import NotificationToast from '@/components/NotificationToast.vue'
import ProjectsView from '@/views/ProjectsView.vue'

const loggedOut = ref(false)

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
