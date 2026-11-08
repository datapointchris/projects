<template>
  <Teleport to="body">
    <div
      data-testid="listing-modal-overlay"
      class="add-edit-modal__overlay"
      :class="{
        visible: visible && !closeMode,
        closing: closeMode === 'cancel',
        'closing-success': closeMode === 'success',
      }"
      @click="handleClose"
      @animationend="onOverlayAnimationEnd"></div>
    <div
      data-testid="listing-modal"
      class="listing-modal__window"
      :class="{
        visible: visible && !closeMode,
        closing: closeMode === 'cancel',
        'closing-success': closeMode === 'success',
      }"
      @animationend="onAnimationEnd">
      <slot
        :handle-close="handleClose"
        :handle-success="handleSuccess"></slot>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'

const props = defineProps<{
  visible: boolean
  focusRef?: HTMLElement | null
}>()

const emit = defineEmits<{
  close: []
}>()

const closeMode = ref<'cancel' | 'success' | null>(null)

watch(
  () => props.visible,
  (val) => {
    if (val) {
      nextTick(() => props.focusRef?.focus())
    }
  },
)

function handleClose() {
  if (!props.visible || closeMode.value) return
  closeMode.value = 'cancel'
}

function handleSuccess() {
  closeMode.value = 'success'
}

function onAnimationEnd(event: AnimationEvent) {
  if (event.animationName === 'window-cancel-squeeze' || event.animationName === 'window-success-squeeze') {
    emit('close')
  }
}

function onOverlayAnimationEnd(event: AnimationEvent) {
  if (event.animationName === 'overlay-out') {
    closeMode.value = null
  }
}
</script>
