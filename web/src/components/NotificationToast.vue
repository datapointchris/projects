<template>
  <Teleport to="body">
    <div
      v-show="containerVisible"
      class="flash-messages">
      <TransitionGroup
        name="notification"
        tag="div"
        class="flash-messages__list"
        appear>
        <div
          v-if="notifications.length > 1"
          key="dismiss-all"
          class="flash-messages__dismiss-all">
          <button
            class="flash-messages__dismiss-all-button"
            @click="closeAll">
            <i class="fa-solid fa-xmark"></i>
            Dismiss All
          </button>
        </div>
        <div
          v-for="notification in notifications"
          :key="notification.id"
          class="flash-messages__message"
          :class="`flash-messages__message--${notification.category}`">
          <span class="flash-messages__text">{{ notification.message }}</span>
          <button
            class="flash-messages__close-button"
            @click="close(notification.id)">
            <i class="fa-solid fa-xmark"></i>
          </button>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
// Styles are in global SCSS (_flash_messages.scss), not scoped — see comments there for why.
// Container uses v-show + delayed hide so the last notification's leave animation can finish.
import { ref, watch } from 'vue'
import { useNotifications } from '@/composables/useNotifications'

const { notifications, close, closeAll } = useNotifications()

const containerVisible = ref(false)
let hideTimer: ReturnType<typeof setTimeout> | null = null

watch(
  () => notifications.value.length,
  (len) => {
    if (hideTimer) {
      clearTimeout(hideTimer)
      hideTimer = null
    }
    if (len > 0) {
      containerVisible.value = true
    } else {
      hideTimer = setTimeout(() => {
        containerVisible.value = false
      }, 1500)
    }
  },
)
</script>
