<template>
  <div class="projects-page">
    <!-- Left pane: project list -->
    <aside class="projects-page__sidebar">
      <div class="projects-page__sidebar-header">
        <h2>Projects</h2>
        <button
          data-testid="project-add-button"
          class="button"
          @click="showProjectModal = true">
          <span class="button__text"><i class="fa-solid fa-plus"></i></span>
        </button>
      </div>

      <!-- Its own row: the header has no width left for a third control, and a
           select that overflows it lands on top of the items pane. -->
      <NeuSelect
        :model-value="store.statusFilter"
        :options="statusFilterOptions"
        data-testid="project-status-filter"
        class="projects-page__status-filter"
        @update:model-value="store.setStatusFilter($event as ProjectStatusFilter)" />

      <!-- All Projects shortcut -->
      <div
        class="projects-page__project projects-page__project--all"
        :class="{ 'projects-page__project--selected': store.isViewAll }"
        @click="store.fetchAllProjectsWithItems()">
        <i class="fa-solid fa-layer-group projects-page__all-icon"></i>
        <span class="projects-page__project-name">All Projects</span>
      </div>

      <div
        v-if="store.loading"
        class="projects-page__empty">
        Loading...
      </div>
      <div
        v-else-if="store.sortedProjects.length === 0"
        data-testid="project-list-empty"
        class="projects-page__empty">
        {{ store.statusFilter === 'all' ? 'No projects yet' : `No ${store.statusFilter} projects` }}
      </div>
      <draggable
        v-else
        :list="localProjects"
        item-key="id"
        handle=".projects-page__drag-handle"
        ghost-class="projects-page__ghost"
        :animation="150"
        class="projects-page__project-list"
        @end="onProjectDragEnd">
        <template #item="{ element: project }">
          <div
            data-testid="project-item"
            class="projects-page__project"
            :class="{ 'projects-page__project--selected': store.selectedProjectIds.includes(project.id) }"
            @click="selectProject(project.id)">
            <div class="projects-page__drag-handle">
              <i class="fa-solid fa-grip-vertical"></i>
            </div>
            <span class="projects-page__project-name">{{ project.name }}</span>
            <span
              v-if="project.status !== 'active'"
              data-testid="project-status"
              class="projects-page__project-status"
              :title="project.status_reason ?? undefined">
              {{ project.status }}
            </span>
            <span
              data-testid="project-kind"
              class="projects-page__project-kind">
              {{ project.kind }}
            </span>
            <span class="projects-page__project-count">{{ project.item_count }}</span>
            <button
              class="projects-page__multi-select-btn"
              :title="store.selectedProjectIds.includes(project.id) ? 'Remove from view' : 'Add to view'"
              @click.stop="store.toggleProjectSelection(project.id)">
              <i :class="store.selectedProjectIds.includes(project.id) ? 'fa-solid fa-square-check' : 'fa-regular fa-square'"></i>
            </button>
          </div>
        </template>
      </draggable>
    </aside>

    <!-- Right pane: items for selected project -->
    <main
      class="projects-page__items"
      :class="{ 'projects-page__items--fading': itemsFading }">
      <!-- Search bar (always visible) -->
      <form
        class="projects-page__search"
        @submit.prevent="handleSearch">
        <input
          v-model="searchQuery"
          data-testid="project-search-input"
          type="text"
          class="textbox"
          placeholder="Search all items..." />
        <button
          type="submit"
          data-testid="project-search-button"
          class="button">
          <span class="button__text"><i class="fa-solid fa-search"></i></span>
        </button>
        <button
          v-if="isSearchActive"
          type="button"
          data-testid="project-search-clear"
          class="button button--danger"
          @click="clearSearch">
          <span class="button__text button__text--danger">Clear</span>
        </button>
      </form>

      <!-- Search results mode -->
      <template v-if="isSearchActive">
        <p class="projects-page__search-status">
          {{ searchResults.length }} result{{ searchResults.length !== 1 ? 's' : '' }} for "{{ activeSearchQuery }}"
        </p>
        <div
          v-if="searchLoading"
          class="projects-page__empty">
          Searching...
        </div>
        <div
          v-else-if="searchResults.length === 0"
          class="projects-page__empty">
          No results found
        </div>
        <div
          v-else
          class="projects-page__item-list">
          <div
            v-for="item in searchResults"
            :key="item.id"
            data-testid="project-search-result"
            class="projects-page__item"
            :class="{
              'projects-page__item--completed': item.completed,
              'projects-page__item--archived': item.archived,
            }">
            <i
              :class="item.completed ? 'fa-solid fa-circle-check' : 'fa-regular fa-circle'"
              class="projects-page__search-icon"></i>
            <div class="projects-page__item-content">
              <span class="projects-page__item-title">{{ item.title }}</span>
              <span
                v-if="item.notes"
                class="projects-page__item-notes">
                {{ item.notes }}
              </span>
            </div>
            <div class="projects-page__item-actions">
              <ActionButton
                data-testid="search-result-projects-button"
                icon="fa-solid fa-folder-open"
                title="Manage projects"
                @click="openProjectsModal(item)" />
              <ActionButton
                data-testid="search-result-deps-button"
                icon="fa-solid fa-link"
                title="Manage dependencies"
                @click="openDepsModal(item)" />
            </div>
          </div>
        </div>
      </template>

      <!-- Normal project items mode -->
      <template v-else>
        <div
          v-if="store.selectedProjectIds.length === 0 && !store.isViewAll"
          class="projects-page__empty">
          Select a project to view items
        </div>
        <template v-else>
          <!-- Single-project header -->
          <div
            v-if="store.selectedProjectIds.length === 1 && !store.isViewAll"
            class="projects-page__items-header">
            <div class="projects-page__items-header-title">
              <h2>{{ store.selectedProject?.name }}</h2>
              <span
                v-if="store.selectedProject?.description"
                class="projects-page__project-desc"
                :class="{ 'projects-page__project-desc--open': descOpen }">
                {{ store.selectedProject!.description }}
              </span>
              <span
                v-if="store.selectedProject && store.selectedProject.status !== 'active'"
                data-testid="project-closure"
                class="projects-page__project-closure">
                {{ store.selectedProject.status
                }}{{ store.selectedProject.status_reason ? ` — ${store.selectedProject.status_reason}` : '' }}
              </span>
            </div>
            <div class="projects-page__header-actions">
              <ActionButton
                v-if="store.selectedProject?.description"
                icon="fa-solid fa-chevron-down"
                title="Toggle description"
                :rotated="descOpen"
                @click="descOpen = !descOpen" />
              <ActionButton
                data-testid="project-edit-button"
                icon="fa-solid fa-pen"
                variant="warning"
                title="Edit project"
                @click="store.selectedProject && openEditProject(store.selectedProject)" />
              <ActionButton
                v-if="store.selectedProject?.status === 'active'"
                data-testid="project-complete-button"
                icon="fa-solid fa-check"
                variant="success"
                title="Complete project — finished, and hidden from the list"
                @click="store.selectedProject && handleCompleteProject(store.selectedProject)" />
              <ActionButton
                v-if="store.selectedProject?.status === 'active'"
                data-testid="project-shelve-button"
                icon="fa-solid fa-box-archive"
                title="Shelve project — set aside for someday, hidden but not closed"
                @click="store.selectedProject && handleShelveProject(store.selectedProject)" />
              <ActionButton
                v-if="store.selectedProject?.status === 'active'"
                data-testid="project-drop-button"
                icon="fa-solid fa-ban"
                variant="warning"
                title="Drop project — closed without being finished"
                @click="openDropProject()" />
              <ActionButton
                v-else
                data-testid="project-reopen-button"
                icon="fa-solid fa-rotate-left"
                variant="success"
                title="Reopen project"
                @click="store.selectedProject && handleReopenProject(store.selectedProject)" />
              <ActionButton
                data-testid="project-delete-button"
                icon="fa-solid fa-trash"
                variant="danger"
                title="Delete project"
                @click="store.selectedProject && handleDeleteProject(store.selectedProject.id)" />
              <button
                data-testid="project-item-add-button"
                class="button"
                @click="showItemModal = true">
                <span class="button__text">
                  <i class="fa-solid fa-plus"></i>
                  Add Item
                </span>
              </button>
            </div>
          </div>

          <div
            v-if="store.itemsLoading"
            class="projects-page__empty">
            Loading...
          </div>

          <!-- ── Single project: draggable flat list ── -->
          <template v-else-if="store.selectedProjectIds.length === 1 && !store.isViewAll">
            <div
              v-if="store.sortedItems.length === 0"
              class="projects-page__empty">
              No items in this project
            </div>
            <draggable
              v-else
              :list="localItems"
              item-key="id"
              handle=".projects-page__drag-handle"
              ghost-class="projects-page__ghost"
              :animation="150"
              class="projects-page__item-list"
              @end="onItemDragEnd">
              <template #item="{ element: item }">
                <div
                  data-testid="project-item-row"
                  :data-item-id="item.id"
                  class="projects-page__item"
                  :class="{
                    'projects-page__item--completed': item.completed,
                    'projects-page__item--archived': item.archived,
                    'projects-page__item--blocked': isBlocked(item.id),
                    'projects-page__item--flash': flashItemId === item.id,
                  }">
                  <div class="projects-page__drag-handle">
                    <i class="fa-solid fa-grip-vertical"></i>
                  </div>
                  <button
                    data-testid="project-item-complete-button"
                    class="projects-page__check"
                    :title="item.completed ? 'Mark incomplete' : 'Mark complete'"
                    @click="handleToggleComplete(item)">
                    <i :class="item.completed ? 'fa-solid fa-circle-check' : 'fa-regular fa-circle'"></i>
                  </button>
                  <div class="projects-page__item-content">
                    <span class="projects-page__item-title">{{ item.title }}</span>
                    <span
                      v-if="item.notes"
                      class="projects-page__item-notes">
                      {{ item.notes }}
                    </span>
                    <span
                      v-if="getBlockers(item.id).length > 0"
                      class="projects-page__item-blockers">
                      <i class="fa-solid fa-lock"></i>
                      <template
                        v-for="(blocker, idx) in getBlockers(item.id)"
                        :key="blocker.id">
                        <span
                          class="projects-page__blocker-link"
                          @click.stop="handleBlockerClick(blocker.id)">
                          {{ blocker.title }}
                        </span>
                        <span v-if="idx < getBlockers(item.id).length - 1">,</span>
                      </template>
                    </span>
                    <ProjectItemTasks :item-id="item.id" />
                  </div>
                  <div class="projects-page__item-actions">
                    <ActionButton
                      data-testid="project-item-deps-button"
                      icon="fa-solid fa-link"
                      title="Manage dependencies"
                      @click="openDepsModal(item)" />
                    <ActionButton
                      data-testid="project-item-projects-button"
                      icon="fa-solid fa-folder-open"
                      title="Manage projects"
                      @click="openProjectsModal(item)" />
                    <ActionButton
                      data-testid="project-item-edit-button"
                      icon="fa-solid fa-pen"
                      variant="warning"
                      title="Edit item"
                      @click="openEditItem(item)" />
                    <ActionButton
                      data-testid="project-item-archive-button"
                      icon="fa-solid fa-box-archive"
                      title="Archive item"
                      @click="handleArchiveItem(item.id)" />
                    <ActionButton
                      data-testid="project-item-delete-button"
                      icon="fa-solid fa-trash"
                      variant="danger"
                      title="Delete item"
                      @click="handleDeleteItem(item.id)" />
                  </div>
                </div>
              </template>
            </draggable>
          </template>

          <!-- ── All Projects: grouped by project ── -->
          <template v-else>
            <div
              v-for="group in store.projectGroups"
              :key="group.project.id"
              class="projects-page__project-group">
              <div class="projects-page__group-header">
                <h2>{{ group.project.name }}</h2>
              </div>
              <div
                v-if="group.items.length === 0"
                class="projects-page__empty projects-page__empty--indent">
                No items
              </div>
              <div
                v-else
                class="projects-page__item-list">
                <div
                  v-for="item in group.items"
                  :key="`${item.id}-${group.project.id}`"
                  :data-item-id="item.id"
                  class="projects-page__item"
                  :class="{
                    'projects-page__item--completed': item.completed,
                    'projects-page__item--archived': item.archived,
                    'projects-page__item--blocked': isBlocked(item.id),
                    'projects-page__item--flash': flashItemId === item.id,
                  }">
                  <button
                    class="projects-page__check"
                    :title="item.completed ? 'Mark incomplete' : 'Mark complete'"
                    @click="handleToggleComplete(item)">
                    <i :class="item.completed ? 'fa-solid fa-circle-check' : 'fa-regular fa-circle'"></i>
                  </button>
                  <div class="projects-page__item-content">
                    <span class="projects-page__item-title">{{ item.title }}</span>
                    <span
                      v-if="item.notes"
                      class="projects-page__item-notes">
                      {{ item.notes }}
                    </span>
                    <span
                      v-if="getBlockers(item.id).length > 0"
                      class="projects-page__item-blockers">
                      <i class="fa-solid fa-lock"></i>
                      <template
                        v-for="(blocker, idx) in getBlockers(item.id)"
                        :key="blocker.id">
                        <span
                          class="projects-page__blocker-link"
                          @click.stop="handleBlockerClick(blocker.id)">
                          {{ blocker.title }}
                        </span>
                        <span v-if="idx < getBlockers(item.id).length - 1">,</span>
                      </template>
                    </span>
                    <ProjectItemTasks :item-id="item.id" />
                  </div>
                  <div class="projects-page__item-actions">
                    <ActionButton
                      icon="fa-solid fa-link"
                      title="Manage dependencies"
                      @click="openDepsModal(item)" />
                    <ActionButton
                      icon="fa-solid fa-folder-open"
                      title="Manage projects"
                      @click="openProjectsModal(item)" />
                    <ActionButton
                      icon="fa-solid fa-pen"
                      variant="warning"
                      title="Edit item"
                      @click="openEditItem(item)" />
                    <ActionButton
                      icon="fa-solid fa-box-archive"
                      title="Archive item"
                      @click="handleArchiveItem(item.id)" />
                    <ActionButton
                      icon="fa-solid fa-trash"
                      variant="danger"
                      title="Delete item"
                      @click="handleDeleteItem(item.id)" />
                  </div>
                </div>
              </div>
            </div>
          </template>
        </template>
      </template>
    </main>

    <!-- Modals -->
    <AddEditProjectModal
      :visible="showProjectModal"
      :edit-data="editProjectTarget"
      @close="closeProjectModal"
      @create="handleCreateProject"
      @update="handleUpdateProject" />

    <AddEditProjectItemModal
      :visible="showItemModal"
      :edit-data="editItemTarget"
      :project-id="store.selectedProjectId"
      @close="closeItemModal"
      @create="handleCreateItem"
      @update="handleUpdateItem" />

    <ManageDependenciesModal
      :visible="showDepsModal"
      :item-id="depsTargetId"
      :item-title="depsTargetTitle"
      :project-items="store.sortedItems"
      @close="closeDepsModal"
      @updated="onDepsUpdated" />

    <DropProjectModal
      :visible="showDropModal"
      :project-name="store.selectedProject?.name ?? ''"
      @close="showDropModal = false"
      @drop="handleDropProject" />

    <ManageProjectsModal
      :visible="showProjectsModal"
      :item-id="projectsTargetId"
      :item-title="projectsTargetTitle"
      @close="closeProjectsModal"
      @updated="onDepsUpdated" />
  </div>
</template>

<script setup lang="ts">
import { ref, watch, nextTick, onMounted } from 'vue'
import draggable from 'vuedraggable'
import { useProjectsStore } from '@/stores/projects'
import { useNotifications } from '@/composables/useNotifications'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import type {
  Project,
  ProjectWithItemCount,
  ProjectCreate,
  ProjectKind,
  ProjectStatus,
  ProjectStatusFilter,
  ProjectUpdate,
  ProjectItemInProject,
  ProjectItemCreate,
  ProjectItemUpdate,
  ProjectItem,
} from '@/api/client'
import AddEditProjectModal from '@/components/projects/AddEditProjectModal.vue'
import DropProjectModal from '@/components/projects/DropProjectModal.vue'
import AddEditProjectItemModal from '@/components/projects/AddEditProjectItemModal.vue'
import ManageDependenciesModal from '@/components/projects/ManageDependenciesModal.vue'
import ManageProjectsModal from '@/components/projects/ManageProjectsModal.vue'
import ProjectItemTasks from '@/components/projects/ProjectItemTasks.vue'
import ActionButton from '@/components/ActionButton.vue'
import NeuSelect from '@/components/NeuSelect.vue'

const statusFilterOptions = [
  { value: 'active', label: 'Active' },
  { value: 'someday', label: 'Someday' },
  { value: 'completed', label: 'Completed' },
  { value: 'dropped', label: 'Dropped' },
  { value: 'all', label: 'All' },
]

const store = useProjectsStore()
const { show: notify } = useNotifications()

// --- Local arrays for vuedraggable (synced with store) ---
const localProjects = ref<ProjectWithItemCount[]>([])
const localItems = ref<ProjectItemInProject[]>([])

watch(
  () => store.sortedProjects,
  (val) => {
    localProjects.value = [...val]
  },
  { immediate: true },
)

watch(
  () => store.sortedItems,
  (val) => {
    localItems.value = [...val]
  },
  { immediate: true },
)

// --- Project modal state ---
const showProjectModal = ref(false)
const editProjectTarget = ref<{
  id: string
  name: string
  description?: string
  kind: ProjectKind
} | null>(null)

const showDropModal = ref(false)

// --- Item modal state ---
const showItemModal = ref(false)
const editItemTarget = ref<{ id: string; title: string; notes?: string } | null>(null)

// --- Dependencies modal state ---
const showDepsModal = ref(false)
const depsTargetId = ref<string | null>(null)
const depsTargetTitle = ref('')

// --- Projects modal state ---
const showProjectsModal = ref(false)
const projectsTargetId = ref<string | null>(null)
const projectsTargetTitle = ref('')

// --- Description expand state ---
const descOpen = ref(false)

watch(
  () => store.selectedProjectId,
  () => {
    descOpen.value = false
  },
)

// --- Search state ---
const searchQuery = ref('')
const activeSearchQuery = ref('')
const searchResults = ref<ProjectItem[]>([])
const searchLoading = ref(false)
const isSearchActive = ref(false)

onMounted(() => {
  store.fetchProjects()
})

function selectProject(projectId: string) {
  store.fetchItems(projectId)
}

// --- Drag handlers ---

function onProjectDragEnd() {
  const orderedIds = localProjects.value.map((p) => p.id)
  store.reorderProjects(orderedIds).catch((e: unknown) => {
    const detail = e instanceof ApiError ? e.userMessage : String(e)
    notify(`Reorder failed: ${detail}`, 'error')
  })
}

function onItemDragEnd() {
  const orderedIds = localItems.value.map((i) => i.id)
  store.reorderItems(orderedIds).catch((e: unknown) => {
    const detail = e instanceof ApiError ? e.userMessage : String(e)
    notify(`Reorder failed: ${detail}`, 'error')
  })
}

// --- Blocker helpers ---

function isBlocked(itemId: string): boolean {
  const blockers = store.itemBlockers[itemId]
  return !!blockers && blockers.length > 0
}

function getBlockers(itemId: string): ProjectItem[] {
  return store.itemBlockers[itemId] ?? []
}

const flashItemId = ref<string | null>(null)
const itemsFading = ref(false)

const FADE_DURATION = 300

function sleep(ms: number) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

function flashItem(itemId: string) {
  flashItemId.value = null
  nextTick(() => {
    flashItemId.value = itemId
    const el = document.querySelector(`[data-item-id="${itemId}"]`)
    if (el) {
      el.scrollIntoView({ behavior: 'smooth', block: 'center' })
    }
    setTimeout(() => {
      flashItemId.value = null
    }, 1500)
  })
}

async function handleBlockerClick(blockerId: string) {
  // Check if the blocker is in the current project's items
  const localItem = store.sortedItems.find((i) => i.id === blockerId)
  if (localItem) {
    flashItem(blockerId)
    return
  }

  // Blocker is in another project — fade out, switch, fade in, then flash
  try {
    const response = await api.get<Project[]>(`/project-items/${blockerId}/projects`)
    const targetProject = response.data[0]
    if (!targetProject) return

    // Fade out current items
    itemsFading.value = true
    await sleep(FADE_DURATION)

    // Switch project while faded out
    await store.fetchItems(targetProject.id)
    await nextTick()

    // Fade back in
    itemsFading.value = false
    await sleep(FADE_DURATION)

    flashItem(blockerId)
  } catch {
    itemsFading.value = false
    notify('Could not navigate to blocker', 'error')
  }
}

// --- Project handlers ---

function openEditProject(project: ProjectWithItemCount) {
  editProjectTarget.value = {
    id: project.id,
    name: project.name,
    description: project.description,
    kind: project.kind,
  }
  showProjectModal.value = true
}

function closeProjectModal() {
  showProjectModal.value = false
  editProjectTarget.value = null
}

async function handleCreateProject(data: ProjectCreate) {
  try {
    await store.createProject(data)
    notify(`${data.name} added`, 'success')
  } catch (e) {
    const detail = e instanceof ApiError ? e.userMessage : String(e)
    notify(`Failed to add project: ${detail}`, 'error')
  }
}

function openDropProject() {
  showDropModal.value = true
}

const STATUS_VERBS: Record<ProjectStatus, { verb: string; done: string }> = {
  active: { verb: 'reopen', done: 'reopened' },
  someday: { verb: 'shelve', done: 'shelved' },
  completed: { verb: 'complete', done: 'completed' },
  dropped: { verb: 'drop', done: 'dropped' },
}

async function changeProjectStatus(project: ProjectWithItemCount, status: ProjectStatus, reason?: string) {
  const { verb, done } = STATUS_VERBS[status]
  try {
    await store.setProjectStatus(project.id, status, reason)
    notify(`${project.name} ${done}`, 'success')
  } catch (e) {
    const detail = e instanceof ApiError ? e.userMessage : String(e)
    notify(`Failed to ${verb} project: ${detail}`, 'error')
  }
}

function handleCompleteProject(project: ProjectWithItemCount) {
  return changeProjectStatus(project, 'completed')
}

function handleShelveProject(project: ProjectWithItemCount) {
  return changeProjectStatus(project, 'someday')
}

function handleReopenProject(project: ProjectWithItemCount) {
  return changeProjectStatus(project, 'active')
}

async function handleDropProject(reason: string) {
  showDropModal.value = false
  if (!store.selectedProject) return
  await changeProjectStatus(store.selectedProject, 'dropped', reason)
}

async function handleUpdateProject(id: string, data: ProjectUpdate) {
  const name = store.projects.find((p) => p.id === id)?.name ?? 'Project'
  try {
    await store.updateProject(id, data)
    notify(`${name} updated`, 'success')
  } catch (e) {
    const detail = e instanceof ApiError ? e.userMessage : String(e)
    notify(`Failed to update project: ${detail}`, 'error')
  }
}

async function handleDeleteProject(id: string) {
  const name = store.projects.find((p) => p.id === id)?.name ?? 'Project'
  try {
    await store.removeProject(id)
    notify(`${name} deleted`, 'success')
  } catch (e) {
    const detail = e instanceof ApiError ? e.userMessage : String(e)
    notify(`Failed to delete project: ${detail}`, 'error')
  }
}

// --- Item handlers ---

function openEditItem(item: ProjectItemInProject) {
  editItemTarget.value = { id: item.id, title: item.title, notes: item.notes }
  showItemModal.value = true
}

function closeItemModal() {
  showItemModal.value = false
  editItemTarget.value = null
}

async function handleCreateItem(data: ProjectItemCreate) {
  try {
    await store.createItem(data)
    notify(`${data.title} added`, 'success')
  } catch (e) {
    const detail = e instanceof ApiError ? e.userMessage : String(e)
    notify(`Failed to add item: ${detail}`, 'error')
  }
}

async function handleUpdateItem(id: string, data: ProjectItemUpdate) {
  const title = store.items.find((i) => i.id === id)?.title ?? 'Item'
  try {
    await store.updateItem(id, data)
    notify(`${title} updated`, 'success')
  } catch (e) {
    const detail = e instanceof ApiError ? e.userMessage : String(e)
    notify(`Failed to update item: ${detail}`, 'error')
  }
}

async function handleToggleComplete(item: ProjectItemInProject) {
  try {
    await store.updateItem(item.id, { completed: !item.completed })
    notify(item.completed ? `${item.title} reopened` : `${item.title} completed`, 'success')
    store.fetchItemBlockers()
  } catch (e) {
    if (e instanceof ApiError && e.status === 400) {
      notify(e.userMessage, 'error')
    } else {
      const detail = e instanceof ApiError ? e.userMessage : String(e)
      notify(`Failed to update item: ${detail}`, 'error')
    }
  }
}

async function handleArchiveItem(id: string) {
  const title = store.items.find((i) => i.id === id)?.title ?? 'Item'
  try {
    await store.archiveItem(id)
    notify(`${title} archived`, 'success')
  } catch (e) {
    const detail = e instanceof ApiError ? e.userMessage : String(e)
    notify(`Failed to archive item: ${detail}`, 'error')
  }
}

async function handleDeleteItem(id: string) {
  const title = store.items.find((i) => i.id === id)?.title ?? 'Item'
  try {
    await store.removeItem(id)
    notify(`${title} deleted`, 'success')
  } catch (e) {
    const detail = e instanceof ApiError ? e.userMessage : String(e)
    notify(`Failed to delete item: ${detail}`, 'error')
  }
}

// --- Dependencies modal ---

function openDepsModal(item: { id: string; title: string }) {
  depsTargetId.value = item.id
  depsTargetTitle.value = item.title
  showDepsModal.value = true
}

function closeDepsModal() {
  showDepsModal.value = false
  depsTargetId.value = null
  depsTargetTitle.value = ''
}

function onDepsUpdated() {
  store.fetchItemBlockers()
}

// --- Projects modal ---

function openProjectsModal(item: { id: string; title: string }) {
  projectsTargetId.value = item.id
  projectsTargetTitle.value = item.title
  showProjectsModal.value = true
}

function closeProjectsModal() {
  showProjectsModal.value = false
  projectsTargetId.value = null
  projectsTargetTitle.value = ''
}

// --- Search ---

async function handleSearch() {
  const query = searchQuery.value.trim()
  if (!query) return
  searchLoading.value = true
  isSearchActive.value = true
  activeSearchQuery.value = query
  try {
    searchResults.value = await store.searchItems(query)
  } catch (e) {
    const detail = e instanceof ApiError ? e.userMessage : String(e)
    notify(`Search failed: ${detail}`, 'error')
    searchResults.value = []
  } finally {
    searchLoading.value = false
  }
}

function clearSearch() {
  searchQuery.value = ''
  activeSearchQuery.value = ''
  searchResults.value = []
  isSearchActive.value = false
}
</script>

<style scoped lang="scss">
.projects-page {
  display: grid;
  grid-template-columns: 280px 1fr;
  gap: 1.5rem;
  min-height: calc(100vh - 6rem);

  &__sidebar {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  &__sidebar-header {
    display: flex;
    align-items: center;
    justify-content: space-between;

    h2 {
      margin: 0;
    }
  }

  &__empty {
    color: var(--clr-gray-300);
    font-style: italic;
  }

  &__project-list {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }

  &__project--all {
    margin-bottom: 0.5rem;

    .projects-page__project-name {
      color: var(--clr-text);
    }

    .projects-page__all-icon {
      color: var(--clr-tertiary);
    }

    &:hover .projects-page__project-name,
    &.projects-page__project--selected .projects-page__project-name {
      color: var(--clr-accent-light);
    }
  }

  &__all-icon {
    font-size: 0.85rem;
    width: 20px;
    text-align: center;
    flex-shrink: 0;
  }

  &__project {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.5rem 0.75rem;
    border-radius: var(--radius-md);
    cursor: pointer;
    background-color: var(--clr-primary);
    box-shadow: var(--shadow-card);
    transition:
      box-shadow 0.15s ease,
      color 0.1s ease;

    &:hover {
      box-shadow: var(--shadow-card-hover);

      .projects-page__project-name {
        color: var(--clr-accent-light);
      }
    }

    &--selected {
      box-shadow: var(--shadow-card-active);

      .projects-page__project-name {
        color: var(--clr-accent-light);
      }

      &:hover {
        box-shadow: var(--shadow-card-active);
      }
    }
  }

  &__drag-handle {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 20px;
    cursor: grab;
    color: var(--clr-gray-400);
    font-size: 0.75rem;
    opacity: 0.5;
    transition: opacity var(--transition-default) ease;

    .projects-page__project:hover &,
    .projects-page__item:hover & {
      opacity: 1;
    }

    &:active {
      cursor: grabbing;
    }
  }

  &__project-name {
    flex: 1;
    font-weight: 500;
  }

  &__project-kind {
    color: var(--clr-gray-500);
    font-size: 0.75rem;
    letter-spacing: 0.05em;
    text-transform: uppercase;
  }

  &__project-status {
    color: var(--clr-warning);
    font-size: 0.75rem;
    letter-spacing: 0.05em;
    text-transform: uppercase;
  }

  &__project-closure {
    color: var(--clr-gray-500);
    font-style: italic;
  }

  &__status-filter {
    width: 100%;
  }

  &__project-count {
    color: var(--clr-accent);
    font-size: 0.85rem;
  }

  &__multi-select-btn {
    background: none;
    border: none;
    cursor: pointer;
    padding: 0.1rem 0.15rem;
    font-size: 0.8rem;
    color: var(--clr-subtle);
    flex-shrink: 0;
    transition: color var(--transition-fast) ease;

    .projects-page__project--selected & {
      color: var(--clr-accent-light);
    }

    &:hover {
      color: var(--clr-accent-light);
    }
  }

  &__items {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    transition:
      opacity 0.3s ease,
      transform 0.3s ease;
  }

  &__items--fading {
    opacity: 0;
    transform: translateY(8px);
  }

  &__items-header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 1rem;

    h2 {
      margin: 0;
      color: var(--clr-accent-light);
    }
  }

  &__items-header-title {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    min-width: 0;
    flex: 1;
  }

  &__header-actions {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex-shrink: 0;
    align-self: flex-start;
  }

  &__project-desc {
    color: var(--clr-subtle);
    font-size: 0.85rem;
    max-height: 1.5em;
    overflow: hidden;
    transition: max-height var(--transition-slow) ease;
    flex: 1;

    &--open {
      max-height: 30em;
    }
  }

  // Sunken inset container — like box-contents
  &__item-list {
    border-radius: var(--button-border-radius);
    background-color: color-mix(in oklch, var(--clr-primary) 85%, black);
    box-shadow: var(--shadow-card-active);
    overflow: hidden; // clips border-radius on first/last items
  }

  &__project-group {
    display: flex;
    flex-direction: column;
    gap: 1rem;
    margin-bottom: 1.5rem;
  }

  &__group-header {
    display: flex;
    align-items: baseline;
    gap: 0.75rem;

    h2 {
      margin: 0;
      color: var(--clr-accent-light);
    }
  }

  &__empty--indent {
    padding: 0.75rem;
  }

  // List row — like list-item mixin
  &__item {
    display: flex;
    align-items: flex-start;
    gap: 0.75rem;
    padding: 0.6rem 0.75rem;
    border-bottom: 1px solid var(--clr-gray-800);
    transition: background-color var(--transition-default) ease;

    &:last-child {
      border-bottom: none;
    }

    &:hover {
      background-color: var(--clr-gray-trans-875);
    }

    &--completed {
      .projects-page__item-title {
        text-decoration: line-through;
        color: var(--clr-gray-300);
      }

      .projects-page__item-notes {
        opacity: 0.5;
      }

      .projects-page__check {
        color: var(--clr-success);
      }
    }

    &--archived {
      opacity: 0.45;
    }

    &--blocked {
      .projects-page__item-title {
        color: var(--clr-gray-300);
      }
    }
  }

  &__check {
    background: none;
    border: none;
    cursor: pointer;
    padding: 0;
    font-size: 1.2rem;
    color: var(--clr-gray-200);
    flex-shrink: 0;
    margin-top: 0.1rem;

    &:hover {
      color: var(--clr-accent-light);
    }
  }

  &__item-content {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    min-width: 0;
  }

  &__item-title {
    font-weight: 500;
    color: var(--clr-text);
  }

  &__item-notes {
    color: var(--clr-gray-100);
    font-size: 0.85rem;
  }

  &__item-blockers {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0.35rem;
    color: var(--clr-error);
    font-size: 0.8rem;

    i {
      font-size: 0.7rem;
    }
  }

  &__blocker-link {
    cursor: pointer;
    text-decoration: underline;
    text-decoration-style: dotted;
    text-underline-offset: 2px;

    &:hover {
      color: var(--clr-text);
    }
  }

  &__item--flash {
    animation: item-flash 1.2s ease forwards;
  }

  &__item-actions {
    display: flex;
    gap: 0.25rem;
    flex-shrink: 0;
  }

  // Search
  &__search {
    display: flex;
    gap: 0.5rem;
    align-items: center;

    .textbox {
      flex: 1;
    }
  }

  &__search-status {
    margin: 0;
    color: var(--clr-gray-200);
    font-size: 0.9rem;
  }

  &__search-icon {
    flex-shrink: 0;
    font-size: 1.1rem;
    color: var(--clr-gray-200);
    margin-top: 0.1rem;
  }

  // Drag ghost (the placeholder left behind)
  &__ghost {
    opacity: 0.3;
  }
}

@keyframes item-flash {
  0% {
    box-shadow:
      0 0 20px rgba(255, 255, 255, 0.8),
      0 0 40px rgba(255, 60, 60, 0.6);
  }

  100% {
    box-shadow: var(--shadow-card);
  }
}
</style>
