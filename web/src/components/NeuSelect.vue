<template>
  <div
    ref="container"
    class="neu-select"
    :class="{ 'neu-select--open': isOpen }">
    <button
      ref="trigger"
      type="button"
      class="neu-select__trigger"
      :data-testid="dataTestid"
      @click="toggle"
      @keydown.escape="close">
      <span
        v-if="selectedOption"
        class="neu-select__selected">
        <slot
          name="selected"
          :option="selectedOption">
          {{ selectedOption.label }}
        </slot>
      </span>
      <span
        v-else
        class="neu-select__placeholder">
        {{ placeholder }}
      </span>
      <i
        class="fa-solid fa-chevron-down neu-select__chevron"
        :class="{ 'neu-select__chevron--open': isOpen }"></i>
    </button>
    <Teleport to="body">
      <Transition name="neu-dropdown">
        <div
          v-if="isOpen"
          class="neu-select__dropdown"
          :style="dropdownStyle"
          @click.stop>
          <div
            v-for="opt in options"
            :key="opt.value"
            class="neu-select__option"
            :class="{ 'neu-select__option--selected': modelValue === opt.value }"
            :data-testid="dataTestid ? `${dataTestid}-option-${opt.value}` : undefined"
            @click="select(opt)">
            <slot
              name="option"
              :option="opt">
              {{ opt.label }}
            </slot>
          </div>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>

<script setup lang="ts" generic="T extends string | number">
import { ref, computed, nextTick, onMounted, onBeforeUnmount } from 'vue'

export interface NeuSelectOption<V = string | number> {
  value: V
  label: string
  [key: string]: unknown
}

const props = defineProps<{
  modelValue: T | null
  options: NeuSelectOption<T>[]
  placeholder?: string
  dataTestid?: string
}>()

const emit = defineEmits<{
  'update:modelValue': [value: T]
}>()

const isOpen = ref(false)
const container = ref<HTMLElement | null>(null)
const trigger = ref<HTMLElement | null>(null)
const dropdownStyle = ref<Record<string, string>>({})

const selectedOption = computed(() => props.options.find((o) => o.value === props.modelValue) ?? null)

function updateDropdownPosition() {
  if (!trigger.value) return
  const rect = trigger.value.getBoundingClientRect()
  const gap = 8
  const viewportPadding = 8
  const spaceBelow = window.innerHeight - rect.bottom - gap - viewportPadding
  const maxHeight = Math.max(120, Math.min(720, spaceBelow))
  dropdownStyle.value = {
    top: `${rect.bottom + gap}px`,
    left: `${rect.left}px`,
    maxHeight: `${maxHeight}px`,
  }
}

function toggle() {
  isOpen.value = !isOpen.value
  if (isOpen.value) {
    nextTick(updateDropdownPosition)
  }
}

function close() {
  isOpen.value = false
}

function select(opt: NeuSelectOption<T>) {
  emit('update:modelValue', opt.value)
  isOpen.value = false
}

function handleClickOutside(e: MouseEvent) {
  const target = e.target as Node
  if (!container.value?.contains(target) && !(target as Element).closest?.('.neu-select__dropdown')) {
    isOpen.value = false
  }
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', handleClickOutside)
})
</script>

<style lang="scss" scoped>
@use 'components/buttons';

.neu-select {
  position: relative;
  display: inline-block;
}

.neu-select__trigger {
  @include buttons.neu-button($hover-transform: scale(1.01));
  display: flex;
  align-items: center;
  gap: var(--space-xs);
  padding: var(--space-3xs) var(--space-s);
  font-size: var(--fs-400);
  min-width: 160px;
  cursor: pointer;
  border: none;
  font-family: inherit;
}

.neu-select__placeholder {
  color: var(--clr-gray-500);
}

.neu-select__selected {
  flex: 1;
  text-align: left;
}

.neu-select__chevron {
  font-size: var(--fs-300);
  color: var(--clr-gray-400);
  transition: transform var(--transition-medium);
}

.neu-select__chevron--open {
  transform: rotate(180deg);
}
</style>

<!-- Unscoped: dropdown is teleported to body, scoped attributes won't reach it -->
<style lang="scss">
@use 'components/buttons';

.neu-select__dropdown {
  position: fixed;
  min-width: 200px;
  width: max-content;
  overflow-y: auto;
  scrollbar-width: none;
  &::-webkit-scrollbar {
    display: none;
  }
  z-index: 100;
  display: flex;
  flex-direction: column;
  gap: var(--space-3xs);
  padding: var(--space-xs);
  border-radius: var(--button-border-radius);
  background-color: var(--clr-primary--darker);
  border-top: 2px solid var(--clr-gray-400);
  border-left: 2px solid var(--clr-gray-400);
  border-right: 1px solid var(--clr-gray-700);
  border-bottom: 1px solid var(--clr-gray-700);

  // prettier-ignore
  box-shadow:
        var(--deep-3d-box-shadow),
        0 20px 40px rgba(0, 0, 0, 0.5),
        0 10px 20px rgba(0, 0, 0, 0.3),
        inset 0 1px 0 rgba(255, 255, 255, 0.08);
}

.neu-select__option {
  @include buttons.neu-button($active-class: '--selected', $hover-transform: scale(1.02));
  padding: var(--space-3xs) var(--space-s);
  font-size: var(--fs-400);
  cursor: pointer;
  white-space: nowrap;
}

// Dropdown transition
.neu-dropdown-enter-active {
  transition: all var(--transition-default) ease-out;
}

.neu-dropdown-leave-active {
  transition: all var(--transition-fast) ease-in;
}

.neu-dropdown-enter-from {
  opacity: 0;
  transform: translateY(-8px) scale(0.97);
}

.neu-dropdown-leave-to {
  opacity: 0;
  transform: translateY(-4px) scale(0.99);
}
</style>
