<template>
  <ListingModal
    :visible="visible"
    @close="handleClose">
    <template #default="{ handleClose: closeModal }">
      <div class="manage-deps">
        <h2>Dependencies</h2>
        <p class="manage-deps__subtitle">{{ itemTitle }}</p>

        <div
          v-if="loading"
          class="manage-deps__empty">
          Loading...
        </div>

        <template v-else>
          <!-- Current dependencies -->
          <div class="manage-deps__section">
            <h3>Blocked by</h3>
            <div
              v-if="currentDeps.length === 0 && crossProjectDepCount === 0"
              class="manage-deps__empty">
              No dependencies
            </div>
            <div
              v-for="dep in currentDeps"
              :key="dep.id"
              class="manage-deps__row">
              <i
                :class="dep.completed ? 'fa-solid fa-circle-check' : 'fa-regular fa-circle'"
                class="manage-deps__status-icon"></i>
              <span
                class="manage-deps__title"
                :class="{ 'manage-deps__title--completed': dep.completed }">
                {{ dep.title }}
              </span>
              <button
                data-testid="dependency-remove-button"
                class="manage-deps__btn manage-deps__btn--remove"
                title="Remove dependency"
                @click="handleRemoveDep(dep.id)">
                <i class="fa-solid fa-xmark"></i>
              </button>
            </div>
            <div
              v-if="crossProjectDepCount > 0"
              class="manage-deps__note">
              + {{ crossProjectDepCount }} in other projects
            </div>
          </div>

          <!-- Add dependency -->
          <div class="manage-deps__section">
            <h3>Add dependency</h3>
            <div
              v-if="availableItems.length === 0"
              class="manage-deps__empty">
              No available items
            </div>
            <div
              v-for="item in availableItems"
              :key="item.id"
              class="manage-deps__row">
              <span class="manage-deps__title">{{ item.title }}</span>
              <button
                data-testid="dependency-add-button"
                class="manage-deps__btn manage-deps__btn--add"
                title="Add as dependency"
                @click="handleAddDep(item.id)">
                <i class="fa-solid fa-plus"></i>
              </button>
            </div>
          </div>

          <div
            v-if="depError"
            class="manage-deps__error">
            {{ depError }}
          </div>
        </template>

        <div class="manage-deps__footer">
          <button
            class="button"
            @click="closeModal()">
            <span class="button__text">Close</span>
          </button>
        </div>
      </div>
    </template>
  </ListingModal>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import ListingModal from '@/components/ListingModal.vue'
import { useProjectsStore } from '@/stores/projects'
import { ApiError } from '@/api/errors'
import type { ProjectItemInProject, ProjectItemDetail } from '@/api/client'

const props = defineProps<{
  visible: boolean
  itemId: string | null
  itemTitle: string
  projectItems: ProjectItemInProject[]
}>()

const emit = defineEmits<{
  close: []
  updated: []
}>()

const store = useProjectsStore()
const loading = ref(false)
const depError = ref<string | null>(null)
const itemDetail = ref<ProjectItemDetail | null>(null)

const currentDeps = computed(() => {
  if (!itemDetail.value) return []
  const depIds = new Set(itemDetail.value.dependency_ids)
  return props.projectItems.filter((item) => depIds.has(item.id))
})

const crossProjectDepCount = computed(() => {
  if (!itemDetail.value) return 0
  const projectItemIds = new Set(props.projectItems.map((i) => i.id))
  return itemDetail.value.dependency_ids.filter((id) => !projectItemIds.has(id)).length
})

const availableItems = computed(() => {
  if (!itemDetail.value) return []
  const depIds = new Set(itemDetail.value.dependency_ids)
  return props.projectItems.filter((item) => item.id !== props.itemId && !depIds.has(item.id))
})

watch(
  () => props.visible,
  async (val) => {
    if (val && props.itemId !== null) {
      loading.value = true
      depError.value = null
      try {
        itemDetail.value = await store.fetchItemDetail(props.itemId)
      } catch {
        depError.value = 'Failed to load dependencies'
      } finally {
        loading.value = false
      }
    } else {
      itemDetail.value = null
      depError.value = null
    }
  },
)

function handleClose() {
  emit('close')
}

async function handleAddDep(depId: string) {
  if (props.itemId === null) return
  depError.value = null
  try {
    const detail = await store.addDependency(props.itemId, { depends_on_id: depId })
    itemDetail.value = detail
    emit('updated')
  } catch (e) {
    depError.value = e instanceof ApiError ? e.userMessage : String(e)
  }
}

async function handleRemoveDep(depId: string) {
  if (props.itemId === null) return
  depError.value = null
  try {
    await store.removeDependency(props.itemId, depId)
    itemDetail.value = await store.fetchItemDetail(props.itemId)
    emit('updated')
  } catch (e) {
    depError.value = e instanceof ApiError ? e.userMessage : String(e)
  }
}
</script>

<style scoped lang="scss">
.manage-deps {
  display: flex;
  flex-direction: column;
  gap: 1rem;
  padding: 1.5rem;
  min-width: 360px;

  h2 {
    margin: 0;
  }

  h3 {
    margin: 0;
    font-size: 0.9rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--clr-gray-500);
  }

  &__subtitle {
    margin: -0.5rem 0 0;
    color: var(--clr-gray-500);
    font-style: italic;
  }

  &__section {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  &__empty {
    color: var(--clr-gray-500);
    font-style: italic;
  }

  &__row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.4rem 0.5rem;
    border-radius: var(--radius-default);
    box-shadow: var(--shadow-card);
  }

  &__status-icon {
    flex-shrink: 0;
    color: var(--clr-gray-500);
    font-size: 0.85rem;
  }

  &__title {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;

    &--completed {
      text-decoration: line-through;
      opacity: 0.6;
    }
  }

  &__btn {
    background: none;
    border: none;
    cursor: pointer;
    padding: 0.25rem 0.4rem;
    border-radius: var(--radius-sm);
    flex-shrink: 0;
    font-size: 0.8rem;

    &--remove {
      color: var(--clr-danger, #e74c3c);

      &:hover {
        background: rgba(231, 76, 60, 0.1);
      }
    }

    &--add {
      color: var(--clr-gray-500);

      &:hover {
        color: var(--clr-text);
        background: rgba(128, 128, 128, 0.1);
      }
    }
  }

  &__note {
    color: var(--clr-gray-500);
    font-size: 0.85rem;
    font-style: italic;
    padding-left: 0.5rem;
  }

  &__error {
    color: var(--clr-danger, #e74c3c);
    font-size: 0.9rem;
    padding: 0.5rem;
    border-radius: var(--radius-default);
    background: rgba(231, 76, 60, 0.08);
  }

  &__footer {
    display: flex;
    justify-content: flex-end;
    padding-top: 0.5rem;
  }
}
</style>
