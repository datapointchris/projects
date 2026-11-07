<template>
  <button
    class="action-btn"
    :title="title">
    <i :class="['button-icon', variant, icon, { 'icon--rotated': rotated }]"></i>
  </button>
</template>

<script setup lang="ts">
withDefaults(
  defineProps<{
    icon: string
    title?: string
    variant?: 'warning' | 'danger' | 'success'
    rotated?: boolean
  }>(),
  { rotated: false },
)
</script>

<style scoped lang="scss">
.action-btn {
  background: none;
  border: none;
  cursor: pointer;
  padding: 0.25rem;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 1rem;
}

// <i> is in this component's template so scoped styles apply directly —
// no :deep() needed. The [data-v-xxx] attribute adds one specificity unit,
// making these rules (0,4,0) vs global button-icon:hover at (0,3,0).
// Both transforms must be declared together — CSS can't compose two
// separate transform declarations; last writer wins the whole property.
.icon--rotated {
  transform: rotate(180deg);
}

.icon--rotated:hover:not(.pressed) {
  transform: scale(1.8) rotate(180deg);
}
</style>
