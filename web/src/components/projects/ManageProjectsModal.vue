<template>
  <ListingModal
    :visible="visible"
    @close="handleClose">
    <template #default="{ handleClose: closeModal }">
      <div class="manage-projects">
        <h2>Project Membership</h2>
        <p class="manage-projects__subtitle">{{ itemTitle }}</p>

        <div
          v-if="loading"
          class="manage-projects__empty">
          Loading...
        </div>

        <template v-else>
          <!-- Current project memberships -->
          <div class="manage-projects__section">
            <h3>Member of</h3>
            <div
              v-if="currentProjects.length === 0"
              class="manage-projects__empty">
              No projects
            </div>
            <div
              v-for="project in currentProjects"
              :key="project.id"
              class="manage-projects__row">
              <span class="manage-projects__name">{{ project.name }}</span>
              <button
                data-testid="membership-remove-button"
                class="manage-projects__btn manage-projects__btn--remove"
                :disabled="currentProjects.length <= 1"
                :title="currentProjects.length <= 1 ? 'Cannot remove from last project' : 'Remove from project'"
                @click="handleRemoveFromProject(project.id)">
                <i class="fa-solid fa-xmark"></i>
              </button>
            </div>
          </div>

          <!-- Available projects to add to -->
          <div class="manage-projects__section">
            <h3>Add to project</h3>
            <div
              v-if="availableProjects.length === 0"
              class="manage-projects__empty">
              No other projects
            </div>
            <div
              v-for="project in availableProjects"
              :key="project.id"
              class="manage-projects__row">
              <span class="manage-projects__name">{{ project.name }}</span>
              <button
                data-testid="membership-add-button"
                class="manage-projects__btn manage-projects__btn--add"
                title="Add to project"
                @click="handleAddToProject(project.id)">
                <i class="fa-solid fa-plus"></i>
              </button>
            </div>
          </div>

          <div
            v-if="projError"
            class="manage-projects__error">
            {{ projError }}
          </div>
        </template>

        <div class="manage-projects__footer">
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
import type { ProjectItemDetail, Project } from '@/api/client'

const props = defineProps<{
  visible: boolean
  itemId: string | null
  itemTitle: string
}>()

const emit = defineEmits<{
  close: []
  updated: []
}>()

const store = useProjectsStore()
const loading = ref(false)
const projError = ref<string | null>(null)
const itemDetail = ref<ProjectItemDetail | null>(null)

const currentProjects = computed<Project[]>(() => {
  if (!itemDetail.value) return []
  return itemDetail.value.projects
})

const availableProjects = computed(() => {
  if (!itemDetail.value) return []
  const memberIds = new Set(itemDetail.value.projects.map((p) => p.id))
  return store.projects.filter((p) => !memberIds.has(p.id))
})

watch(
  () => props.visible,
  async (val) => {
    if (val && props.itemId !== null) {
      loading.value = true
      projError.value = null
      try {
        itemDetail.value = await store.fetchItemDetail(props.itemId)
      } catch {
        projError.value = 'Failed to load project memberships'
      } finally {
        loading.value = false
      }
    } else {
      itemDetail.value = null
      projError.value = null
    }
  },
)

function handleClose() {
  emit('close')
}

async function handleAddToProject(projectId: string) {
  if (props.itemId === null) return
  projError.value = null
  try {
    await store.addToProject(props.itemId, { project_id: projectId })
    itemDetail.value = await store.fetchItemDetail(props.itemId)
    emit('updated')
  } catch (e) {
    projError.value = e instanceof ApiError ? e.userMessage : String(e)
  }
}

async function handleRemoveFromProject(projectId: string) {
  if (props.itemId === null) return
  projError.value = null
  try {
    await store.removeFromProject(props.itemId, projectId)
    itemDetail.value = await store.fetchItemDetail(props.itemId)
    emit('updated')
  } catch (e) {
    projError.value = e instanceof ApiError ? e.userMessage : String(e)
  }
}
</script>

<style scoped lang="scss">
.manage-projects {
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

  &__name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
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

      &:hover:not(:disabled) {
        background: rgba(231, 76, 60, 0.1);
      }

      &:disabled {
        opacity: 0.3;
        cursor: not-allowed;
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
