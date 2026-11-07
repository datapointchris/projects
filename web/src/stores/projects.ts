import { ref, computed } from 'vue'
import { defineStore } from 'pinia'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import { createLogger } from '@/utils/logger'
import type {
  ProjectWithItemCount,
  ProjectCreate,
  ProjectUpdate,
  ProjectStatus,
  ProjectStatusFilter,
  ProjectItem,
  ProjectItemCreate,
  ProjectItemUpdate,
  ProjectItemDetail,
  ProjectItemInProject,
  ProjectItemReorder,
  ProjectItemMembershipCreate,
  ProjectItemDependencyCreate,
  ProjectItemTask,
  ProjectItemTaskCreate,
  ProjectItemTaskUpdate,
} from '@/api/client'

const logger = createLogger('ProjectsStore')

// The API narrows this list to open items, so the board asks for the rest back
// explicitly. It renders every state — a completed row keeps the toggle that
// reopens it, and an archived row has styling of its own — and none of that is
// reachable if the default decides for it.
function projectItemsPath(projectId: string) {
  return `/projects/${projectId}/items?status=all`
}

export const useProjectsStore = defineStore('projects', () => {
  // --- Project state ---
  const projects = ref<ProjectWithItemCount[]>([])
  const statusFilter = ref<ProjectStatusFilter>('active')
  const selectedProjectId = ref<string | null>(null)
  const selectedProjectIds = ref<string[]>([])
  const loading = ref(false)
  const error = ref<ApiError | null>(null)

  // --- Item state (scoped to selected project, or all projects) ---
  const items = ref<ProjectItemInProject[]>([])
  const itemsLoading = ref(false)
  const itemBlockers = ref<Record<string, ProjectItem[]>>({})
  const isViewAll = ref(false)
  const projectGroups = ref<{ project: ProjectWithItemCount; items: ProjectItemInProject[] }[]>([])

  // --- Task state (per-item, loaded on demand) ---
  const itemTasks = ref<Record<string, ProjectItemTask[]>>({})

  const sortedProjects = computed(() => [...projects.value].sort((a, b) => a.position - b.position))

  const sortedItems = computed(() => [...items.value].sort((a, b) => a.position - b.position))

  const selectedProject = computed(() => projects.value.find((p) => p.id === selectedProjectId.value) ?? null)

  function clearError() {
    error.value = null
  }

  // --- Project CRUD ---

  async function fetchProjects() {
    loading.value = true
    error.value = null
    try {
      // Always explicit, unlike the CLI: the filter is a control on the page, so
      // leaving it to the server's default would make the select's `active`
      // position mean something the store never said.
      const response = await api.get<ProjectWithItemCount[]>('/projects', {
        params: { status: statusFilter.value },
      })
      projects.value = response.data
      logger.info('projects_fetched', { count: response.data.length, status: statusFilter.value })
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      logger.error('projects_fetch_failed', { detail: apiError.detail, status: apiError.status })
      throw apiError
    } finally {
      loading.value = false
    }
  }

  async function createProject(input: ProjectCreate) {
    error.value = null
    try {
      await api.post('/projects', input)
      await fetchProjects()
      logger.info('project_created', { name: input.name })
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      logger.error('project_create_failed', { detail: apiError.detail, status: apiError.status })
      throw apiError
    }
  }

  async function updateProject(id: string, input: ProjectUpdate) {
    error.value = null
    try {
      await api.patch(`/projects/${id}`, input)
      await fetchProjects()
      logger.info('project_updated', { id })
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      logger.error('project_update_failed', { id, detail: apiError.detail, status: apiError.status })
      throw apiError
    }
  }

  async function setStatusFilter(status: ProjectStatusFilter) {
    if (statusFilter.value === status) return
    statusFilter.value = status
    await fetchProjects()
    // Whatever was selected may no longer be in the list the filter now shows,
    // and a selection pointing at an absent project renders an empty pane with
    // no way back to it.
    for (const id of [...selectedProjectIds.value]) {
      if (!projects.value.some((p) => p.id === id)) dropSelection(id)
    }
  }

  /** Forget a project the list no longer holds, whether closed, filtered out, or deleted. */
  function dropSelection(id: string) {
    projects.value = projects.value.filter((p) => p.id !== id)
    selectedProjectIds.value = selectedProjectIds.value.filter((x) => x !== id)
    if (selectedProjectId.value === id) {
      selectedProjectId.value = selectedProjectIds.value[0] ?? null
      if (selectedProjectIds.value.length === 0) items.value = []
    }
  }

  /**
   * Status transitions, all of them a PATCH — there are no complete/drop/reopen
   * action endpoints, so the server validates the transition in one place.
   * Dropping requires a reason, which is what distinguishes it from deferring.
   */
  async function setProjectStatus(id: string, status: ProjectStatus, reason?: string) {
    const update: ProjectUpdate = { status }
    if (reason !== undefined) update.status_reason = reason
    await updateProject(id, update)
    if (!projects.value.some((p) => p.id === id)) dropSelection(id)
  }

  async function removeProject(id: string) {
    error.value = null
    try {
      await api.delete(`/projects/${id}`)
      dropSelection(id)
      logger.info('project_deleted', { id })
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      logger.error('project_delete_failed', { id, detail: apiError.detail, status: apiError.status })
      throw apiError
    }
  }

  // --- Item operations (scoped to selected project) ---

  async function fetchItems(projectId: string) {
    selectedProjectId.value = projectId
    selectedProjectIds.value = [projectId]
    isViewAll.value = false
    itemsLoading.value = true
    itemBlockers.value = {}
    error.value = null
    try {
      const response = await api.get<ProjectItemInProject[]>(projectItemsPath(projectId))
      items.value = response.data
      logger.info('items_fetched', { project_id: projectId, count: response.data.length })
      fetchItemBlockers()
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      logger.error('items_fetch_failed', {
        project_id: projectId,
        detail: apiError.detail,
        status: apiError.status,
      })
      throw apiError
    } finally {
      itemsLoading.value = false
    }
  }

  async function fetchItemsForProjects(ids: string[]) {
    selectedProjectId.value = ids[0] ?? null
    selectedProjectIds.value = ids
    isViewAll.value = false
    itemsLoading.value = true
    itemBlockers.value = {}
    error.value = null
    try {
      if (projects.value.length === 0) await fetchProjects()
      const projectsToFetch = sortedProjects.value.filter((p) => ids.includes(p.id))
      const groups = await Promise.all(
        projectsToFetch.map(async (project) => {
          const response = await api.get<ProjectItemInProject[]>(projectItemsPath(project.id))
          return { project, items: response.data }
        }),
      )
      projectGroups.value = groups
      const seen = new Set<string>()
      items.value = groups
        .flatMap((g) => g.items)
        .filter((item) => {
          if (seen.has(item.id)) return false
          seen.add(item.id)
          return true
        })
      fetchItemBlockers()
      logger.info('projects_fetched_multi', { ids: ids.length, items: items.value.length })
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      logger.error('projects_fetch_multi_failed', { detail: apiError.detail, status: apiError.status })
      throw apiError
    } finally {
      itemsLoading.value = false
    }
  }

  function toggleProjectSelection(id: string) {
    const idx = selectedProjectIds.value.indexOf(id)
    if (idx >= 0) {
      selectedProjectIds.value = selectedProjectIds.value.filter((x) => x !== id)
    } else {
      selectedProjectIds.value = [...selectedProjectIds.value, id]
    }

    const ids = selectedProjectIds.value
    if (ids.length === 0) {
      selectedProjectId.value = null
      items.value = []
      projectGroups.value = []
      isViewAll.value = false
    } else if (ids.length === 1 && ids[0] !== undefined) {
      fetchItems(ids[0])
    } else {
      fetchItemsForProjects(ids)
    }
  }

  async function fetchAllProjectsWithItems() {
    selectedProjectId.value = null
    selectedProjectIds.value = []
    isViewAll.value = true
    itemsLoading.value = true
    itemBlockers.value = {}
    error.value = null
    try {
      if (projects.value.length === 0) await fetchProjects()
      const groups = await Promise.all(
        sortedProjects.value.map(async (project) => {
          const response = await api.get<ProjectItemInProject[]>(projectItemsPath(project.id))
          return { project, items: response.data }
        }),
      )
      projectGroups.value = groups
      // Deduplicated flat list for blocker fetching (items can be in multiple projects)
      const seen = new Set<string>()
      items.value = groups
        .flatMap((g) => g.items)
        .filter((item) => {
          if (seen.has(item.id)) return false
          seen.add(item.id)
          return true
        })
      fetchItemBlockers()
      logger.info('all_projects_fetched', { groups: groups.length, items: items.value.length })
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      logger.error('all_projects_fetch_failed', { detail: apiError.detail, status: apiError.status })
      throw apiError
    } finally {
      itemsLoading.value = false
    }
  }

  async function createItem(input: ProjectItemCreate) {
    error.value = null
    try {
      const response = await api.post<ProjectItemDetail>('/project-items', input)
      logger.info('item_created', { id: response.data.id, title: input.title })
      // Refresh both projects (item counts) and items list
      await fetchProjects()
      if (selectedProjectId.value !== null) {
        await fetchItems(selectedProjectId.value)
      }
      return response.data
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      logger.error('item_create_failed', { detail: apiError.detail, status: apiError.status })
      throw apiError
    }
  }

  async function updateItem(id: string, input: ProjectItemUpdate) {
    error.value = null
    try {
      const response = await api.patch<ProjectItem>(`/project-items/${id}`, input)
      const index = items.value.findIndex((i) => i.id === id)
      const existing = items.value[index]
      if (index !== -1 && existing) {
        items.value[index] = { ...response.data, position: existing.position }
      }
      logger.info('item_updated', { id })
      return response.data
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      logger.error('item_update_failed', { id, detail: apiError.detail, status: apiError.status })
      throw apiError
    }
  }

  async function removeItem(id: string) {
    error.value = null
    try {
      await api.delete(`/project-items/${id}`)
      items.value = items.value.filter((i) => i.id !== id)
      await fetchProjects()
      logger.info('item_deleted', { id })
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      logger.error('item_delete_failed', { id, detail: apiError.detail, status: apiError.status })
      throw apiError
    }
  }

  async function reorderItem(id: string, input: ProjectItemReorder) {
    error.value = null
    try {
      await api.patch(`/project-items/${id}/reorder`, input)
      logger.info('item_reordered', { id, project_id: input.project_id, position: input.position })
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      logger.error('item_reorder_failed', { id, detail: apiError.detail, status: apiError.status })
      throw apiError
    }
  }

  async function reorderItems(orderedIds: string[]) {
    if (selectedProjectId.value === null) return
    const projectId = selectedProjectId.value
    error.value = null

    try {
      const updates: Promise<unknown>[] = []

      for (const [index, id] of orderedIds.entries()) {
        const item = items.value.find((i) => i.id === id)
        if (item && item.position !== index) {
          updates.push(api.patch(`/project-items/${id}/reorder`, { project_id: projectId, position: index }))
        }
      }

      if (updates.length > 0) {
        await Promise.all(updates)
        for (const [index, id] of orderedIds.entries()) {
          const item = items.value.find((i) => i.id === id)
          if (item) item.position = index
        }
        logger.info('items_reordered', { project_id: projectId, count: updates.length })
      }
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      logger.error('items_reorder_failed', { detail: apiError.detail })
      await fetchItems(projectId)
      throw apiError
    }
  }

  async function reorderProjects(orderedIds: string[]) {
    error.value = null

    try {
      const updates: Promise<unknown>[] = []

      for (const [index, id] of orderedIds.entries()) {
        const project = projects.value.find((p) => p.id === id)
        if (project && project.position !== index) {
          updates.push(api.patch(`/projects/${id}`, { position: index }))
        }
      }

      if (updates.length > 0) {
        await Promise.all(updates)
        for (const [index, id] of orderedIds.entries()) {
          const project = projects.value.find((p) => p.id === id)
          if (project) project.position = index
        }
        logger.info('projects_reordered', { count: updates.length })
      }
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      logger.error('projects_reorder_failed', { detail: apiError.detail })
      await fetchProjects()
      throw apiError
    }
  }

  async function completeItem(id: string) {
    return updateItem(id, { completed: true })
  }

  async function archiveItem(id: string) {
    return updateItem(id, { archived: true })
  }

  // --- Blocker tracking ---

  async function fetchItemBlockers() {
    const blockerMap: Record<string, ProjectItem[]> = {}

    await Promise.all(
      items.value.map(async (item) => {
        try {
          const response = await api.get<ProjectItem[]>(`/project-items/${item.id}/blockers`)
          if (response.data.length > 0) {
            blockerMap[item.id] = response.data
          }
        } catch {
          // Non-critical — skip blocker info for this item
        }
      }),
    )

    itemBlockers.value = blockerMap
    logger.debug('item_blockers_fetched', { blocked_count: Object.keys(blockerMap).length })
  }

  async function fetchItemDetail(id: string) {
    const response = await api.get<ProjectItemDetail>(`/project-items/${id}`)
    return response.data
  }

  // --- Dependencies ---

  async function addDependency(itemId: string, input: ProjectItemDependencyCreate) {
    error.value = null
    try {
      const response = await api.post<ProjectItemDetail>(`/project-items/${itemId}/dependencies`, input)
      logger.info('dependency_added', { item_id: itemId, depends_on: input.depends_on_id })
      return response.data
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      logger.error('dependency_add_failed', {
        item_id: itemId,
        detail: apiError.detail,
        status: apiError.status,
      })
      throw apiError
    }
  }

  async function removeDependency(itemId: string, depId: string) {
    error.value = null
    try {
      await api.delete(`/project-items/${itemId}/dependencies/${depId}`)
      logger.info('dependency_removed', { item_id: itemId, dep_id: depId })
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      logger.error('dependency_remove_failed', {
        item_id: itemId,
        detail: apiError.detail,
        status: apiError.status,
      })
      throw apiError
    }
  }

  async function getBlockers(itemId: string) {
    try {
      const response = await api.get<ProjectItem[]>(`/project-items/${itemId}/blockers`)
      return response.data
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      logger.error('blockers_fetch_failed', {
        item_id: itemId,
        detail: apiError.detail,
        status: apiError.status,
      })
      throw apiError
    }
  }

  // --- Membership ---

  async function addToProject(itemId: string, input: ProjectItemMembershipCreate) {
    error.value = null
    try {
      await api.post(`/project-items/${itemId}/projects`, input)
      logger.info('item_added_to_project', { item_id: itemId, project_id: input.project_id })
      await fetchProjects()
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      throw apiError
    }
  }

  async function removeFromProject(itemId: string, projectId: string) {
    error.value = null
    try {
      await api.delete(`/project-items/${itemId}/projects/${projectId}`)
      logger.info('item_removed_from_project', { item_id: itemId, project_id: projectId })
      await fetchProjects()
      if (selectedProjectId.value !== null) {
        await fetchItems(selectedProjectId.value)
      }
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      throw apiError
    }
  }

  // --- Search ---

  async function searchItems(query: string) {
    try {
      const response = await api.get<ProjectItem[]>('/project-items/search', {
        params: { q: query },
        holdsTypedInput: true,
      })
      logger.info('items_searched', { query, count: response.data.length })
      return response.data
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      logger.error('items_search_failed', { query, detail: apiError.detail })
      throw apiError
    }
  }

  // --- Item Tasks ---

  async function fetchItemTasks(itemId: string) {
    try {
      const response = await api.get<ProjectItemTask[]>(`/project-items/${itemId}/tasks`)
      itemTasks.value[itemId] = response.data
      return response.data
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      logger.error('item_tasks_fetch_failed', { item_id: itemId, detail: apiError.detail })
      throw apiError
    }
  }

  async function createItemTask(itemId: string, input: ProjectItemTaskCreate) {
    error.value = null
    try {
      const response = await api.post<ProjectItemTask>(`/project-items/${itemId}/tasks`, input)
      logger.info('item_task_created', { item_id: itemId, title: input.title })
      await fetchItemTasks(itemId)
      return response.data
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      logger.error('item_task_create_failed', { item_id: itemId, detail: apiError.detail })
      throw apiError
    }
  }

  async function updateItemTask(itemId: string, taskId: string, input: ProjectItemTaskUpdate) {
    error.value = null
    try {
      const response = await api.patch<ProjectItemTask>(`/project-items/${itemId}/tasks/${taskId}`, input)
      logger.info('item_task_updated', { item_id: itemId, task_id: taskId })
      await fetchItemTasks(itemId)
      return response.data
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      logger.error('item_task_update_failed', { item_id: itemId, task_id: taskId, detail: apiError.detail })
      throw apiError
    }
  }

  async function removeItemTask(itemId: string, taskId: string) {
    error.value = null
    try {
      await api.delete(`/project-items/${itemId}/tasks/${taskId}`)
      logger.info('item_task_deleted', { item_id: itemId, task_id: taskId })
      await fetchItemTasks(itemId)
    } catch (e) {
      const apiError = e instanceof ApiError ? e : new ApiError({ message: String(e), detail: String(e) })
      error.value = apiError
      logger.error('item_task_delete_failed', { item_id: itemId, task_id: taskId, detail: apiError.detail })
      throw apiError
    }
  }

  async function completeItemTask(itemId: string, taskId: string) {
    return updateItemTask(itemId, taskId, { completed: true })
  }

  return {
    // Project state
    projects,
    statusFilter,
    selectedProjectId,
    selectedProjectIds,
    loading,
    error,
    sortedProjects,
    selectedProject,
    // Item state
    items,
    itemsLoading,
    itemBlockers,
    isViewAll,
    projectGroups,
    sortedItems,
    // Task state
    itemTasks,
    // Actions
    clearError,
    fetchProjects,
    createProject,
    updateProject,
    setStatusFilter,
    setProjectStatus,
    removeProject,
    fetchItems,
    fetchItemsForProjects,
    toggleProjectSelection,
    fetchAllProjectsWithItems,
    createItem,
    updateItem,
    removeItem,
    reorderItem,
    reorderItems,
    reorderProjects,
    completeItem,
    archiveItem,
    fetchItemBlockers,
    fetchItemDetail,
    addDependency,
    removeDependency,
    getBlockers,
    addToProject,
    removeFromProject,
    searchItems,
    // Task actions
    fetchItemTasks,
    createItemTask,
    updateItemTask,
    removeItemTask,
    completeItemTask,
  }
})
