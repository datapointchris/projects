import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useProjectsStore } from '../projects'
import { ApiError } from '@/api/errors'

vi.mock('@/api/client', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    delete: vi.fn(),
  },
}))

import { api } from '@/api/client'
const mockApi = vi.mocked(api)

const testProjects = [
  { id: 1, name: 'Music Setup', position: 0, created_at: '2026-01-01T00:00:00Z', item_count: 3 },
  { id: 2, name: 'Home Renovation', position: 1, created_at: '2026-02-01T00:00:00Z', item_count: 2 },
  { id: 3, name: 'Career Goals', position: 2, created_at: '2026-03-01T00:00:00Z', item_count: 0 },
]

const testItems = [
  {
    id: 10,
    title: 'Decide budget',
    notes: 'Check forums',
    completed: false,
    archived: false,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    position: 0,
  },
  {
    id: 11,
    title: 'Research interfaces',
    notes: null,
    completed: false,
    archived: false,
    created_at: '2026-01-02T00:00:00Z',
    updated_at: '2026-01-02T00:00:00Z',
    position: 1,
  },
  {
    id: 12,
    title: 'Buy interface',
    notes: 'Wait for sale',
    completed: true,
    archived: false,
    created_at: '2026-01-03T00:00:00Z',
    updated_at: '2026-01-03T00:00:00Z',
    position: 2,
  },
]

const testItemDetail = {
  id: 10,
  title: 'Decide budget',
  notes: 'Check forums',
  completed: false,
  archived: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  projects: [{ id: 1, name: 'Music Setup', position: 0, created_at: '2026-01-01T00:00:00Z' }],
  dependency_ids: [11],
}

describe('useProjectsStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    // Default: blocker fetches return empty (most tests don't need blockers)
    mockApi.get.mockImplementation((url: string) => {
      if (typeof url === 'string' && url.includes('/blockers')) {
        return Promise.resolve({ data: [] })
      }
      return Promise.resolve({ data: [] })
    })
  })

  // --- createProject ---

  it('creates a project and refetches', async () => {
    mockApi.post.mockResolvedValue({ data: { id: 4, name: 'New', position: 3 } })
    mockApi.get.mockResolvedValue({ data: [...testProjects] })
    const store = useProjectsStore()

    await store.createProject({ name: 'New' })

    expect(mockApi.post).toHaveBeenCalledWith('/projects', { name: 'New' })
    expect(mockApi.get).toHaveBeenCalledWith('/projects', { params: { status: 'active' } })
  })

  // --- updateProject ---

  it('updates a project and refetches', async () => {
    mockApi.patch.mockResolvedValue({ data: { id: 1, name: 'Renamed', position: 0 } })
    mockApi.get.mockResolvedValue({ data: testProjects })
    const store = useProjectsStore()

    await store.updateProject(1, { name: 'Renamed' })

    expect(mockApi.patch).toHaveBeenCalledWith('/projects/1', { name: 'Renamed' })
  })

  // --- removeProject ---

  it('removes a project from local state', async () => {
    mockApi.delete.mockResolvedValue({})
    const store = useProjectsStore()
    store.projects = [...testProjects]

    await store.removeProject(2)

    expect(mockApi.delete).toHaveBeenCalledWith('/projects/2')
    expect(store.projects).toHaveLength(2)
    expect(store.projects.find((p) => p.id === 2)).toBeUndefined()
  })

  it('clears selection when removing selected project', async () => {
    mockApi.delete.mockResolvedValue({})
    const store = useProjectsStore()
    store.projects = [...testProjects]
    store.selectedProjectId = 2
    store.items = [...testItems]

    await store.removeProject(2)

    expect(store.selectedProjectId).toBeNull()
    expect(store.items).toEqual([])
  })

  it('sets error on remove failure', async () => {
    const apiError = new ApiError({ message: 'fail', detail: 'Has orphan items', status: 409 })
    mockApi.delete.mockRejectedValue(apiError)
    const store = useProjectsStore()
    store.projects = [...testProjects]

    await expect(store.removeProject(1)).rejects.toThrow(ApiError)
    expect(store.projects).toHaveLength(3)
  })

  // --- sortedProjects ---

  it('sorts projects by position', () => {
    const store = useProjectsStore()
    store.projects = [testProjects[2]!, testProjects[0]!, testProjects[1]!]

    expect(store.sortedProjects[0]!.name).toBe('Music Setup')
    expect(store.sortedProjects[1]!.name).toBe('Home Renovation')
    expect(store.sortedProjects[2]!.name).toBe('Career Goals')
  })

  // --- fetchItems ---

  it('fetches items for a project and sets state', async () => {
    mockApi.get.mockImplementation((url: string) => {
      if (url === '/projects/1/items?status=all') return Promise.resolve({ data: testItems })
      return Promise.resolve({ data: [] })
    })
    const store = useProjectsStore()

    await store.fetchItems(1)

    expect(store.selectedProjectId).toBe(1)
    expect(store.items).toEqual(testItems)
    expect(store.itemsLoading).toBe(false)
  })

  // --- sortedItems ---

  it('sorts items by position', () => {
    const store = useProjectsStore()
    store.items = [testItems[2]!, testItems[0]!, testItems[1]!]

    expect(store.sortedItems[0]!.title).toBe('Decide budget')
    expect(store.sortedItems[1]!.title).toBe('Research interfaces')
    expect(store.sortedItems[2]!.title).toBe('Buy interface')
  })

  // --- createItem ---

  it('creates an item and refetches projects and items', async () => {
    const newItemDetail = { ...testItemDetail, id: 20, title: 'New item', dependency_ids: [], projects: [] }
    mockApi.post.mockResolvedValue({ data: newItemDetail })
    mockApi.get.mockImplementation((url: string) => {
      if (url === '/projects') return Promise.resolve({ data: testProjects })
      if (url === '/projects/1/items?status=all') return Promise.resolve({ data: testItems })
      return Promise.resolve({ data: [] })
    })
    const store = useProjectsStore()
    store.selectedProjectId = 1

    const result = await store.createItem({ title: 'New item', project_ids: [1] })

    expect(mockApi.post).toHaveBeenCalledWith('/project-items', { title: 'New item', project_ids: [1] })
    expect(result.id).toBe(20)
  })

  // --- updateItem ---

  it('updates an item in place preserving position', async () => {
    const updatedItem = {
      id: 10,
      title: 'Updated',
      notes: 'New',
      completed: false,
      archived: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    }
    mockApi.patch.mockResolvedValue({ data: updatedItem })
    const store = useProjectsStore()
    store.items = [...testItems]

    await store.updateItem(10, { title: 'Updated', notes: 'New' })

    expect(mockApi.patch).toHaveBeenCalledWith('/project-items/10', { title: 'Updated', notes: 'New' })
    const item = store.items.find((i) => i.id === 10)
    expect(item!.title).toBe('Updated')
    expect(item!.position).toBe(0) // position preserved from original
  })

  // --- removeItem ---

  it('removes an item and refetches projects', async () => {
    mockApi.delete.mockResolvedValue({})
    mockApi.get.mockResolvedValue({ data: testProjects })
    const store = useProjectsStore()
    store.items = [...testItems]

    await store.removeItem(11)

    expect(mockApi.delete).toHaveBeenCalledWith('/project-items/11')
    expect(store.items).toHaveLength(2)
    expect(store.items.find((i) => i.id === 11)).toBeUndefined()
  })

  // --- reorderItems ---

  it('sends position updates only for items that moved', async () => {
    mockApi.patch.mockResolvedValue({ data: {} })
    const store = useProjectsStore()
    store.selectedProjectId = 1
    store.items = [...testItems] // positions: 0, 1, 2

    // Reorder: swap first two items
    await store.reorderItems([11, 10, 12])

    // Item 10 moved from position 0 → 1, item 11 from 1 → 0
    // Item 12 stays at position 2 (no call)
    expect(mockApi.patch).toHaveBeenCalledTimes(2)
    expect(mockApi.patch).toHaveBeenCalledWith('/project-items/11/reorder', { project_id: 1, position: 0 })
    expect(mockApi.patch).toHaveBeenCalledWith('/project-items/10/reorder', { project_id: 1, position: 1 })
  })

  // --- reorderProjects ---

  it('sends position updates for moved projects', async () => {
    mockApi.patch.mockResolvedValue({ data: {} })
    const store = useProjectsStore()
    store.projects = [...testProjects]

    await store.reorderProjects([3, 1, 2])

    expect(mockApi.patch).toHaveBeenCalledWith('/projects/3', { position: 0 })
    expect(mockApi.patch).toHaveBeenCalledWith('/projects/1', { position: 1 })
    expect(mockApi.patch).toHaveBeenCalledWith('/projects/2', { position: 2 })
  })

  // --- archiveItem ---

  it('archiveItem calls updateItem with archived: true', async () => {
    const updatedItem = { ...testItems[0], archived: true }
    mockApi.patch.mockResolvedValue({ data: updatedItem })
    const store = useProjectsStore()
    store.items = [...testItems]

    await store.archiveItem(10)

    expect(mockApi.patch).toHaveBeenCalledWith('/project-items/10', { archived: true })
  })

  // --- fetchItemBlockers ---

  it('fetches blockers for all items in project', async () => {
    const blockerItem = { id: 10, title: 'Decide budget', completed: false, archived: false, created_at: '', updated_at: '' }
    mockApi.get.mockImplementation((url: string) => {
      if (url === '/project-items/11/blockers') return Promise.resolve({ data: [blockerItem] })
      if (url.includes('/blockers')) return Promise.resolve({ data: [] })
      return Promise.resolve({ data: [] })
    })
    const store = useProjectsStore()
    store.items = [...testItems]

    await store.fetchItemBlockers()

    expect(store.itemBlockers[11]).toEqual([blockerItem])
    expect(store.itemBlockers[10]).toBeUndefined()
    expect(store.itemBlockers[12]).toBeUndefined()
  })

  // --- fetchItemDetail ---

  it('fetches item detail with projects and dependencies', async () => {
    mockApi.get.mockResolvedValue({ data: testItemDetail })
    const store = useProjectsStore()

    const result = await store.fetchItemDetail(10)

    expect(mockApi.get).toHaveBeenCalledWith('/project-items/10')
    expect(result.projects).toHaveLength(1)
    expect(result.dependency_ids).toEqual([11])
  })

  // --- addDependency ---

  it('adds a dependency and returns updated detail', async () => {
    const updatedDetail = { ...testItemDetail, dependency_ids: [11, 12] }
    mockApi.post.mockResolvedValue({ data: updatedDetail })
    const store = useProjectsStore()

    const result = await store.addDependency(10, { depends_on_id: 12 })

    expect(mockApi.post).toHaveBeenCalledWith('/project-items/10/dependencies', { depends_on_id: 12 })
    expect(result.dependency_ids).toEqual([11, 12])
  })

  // --- addToProject / removeFromProject ---

  it('adds item to project and refetches projects', async () => {
    mockApi.post.mockResolvedValue({ data: { id: 2, name: 'Home Renovation', position: 1, created_at: '' } })
    mockApi.get.mockResolvedValue({ data: testProjects })
    const store = useProjectsStore()

    await store.addToProject(10, { project_id: 2 })

    expect(mockApi.post).toHaveBeenCalledWith('/project-items/10/projects', { project_id: 2 })
    expect(mockApi.get).toHaveBeenCalledWith('/projects', { params: { status: 'active' } })
  })

  it('removes item from project and refetches', async () => {
    mockApi.delete.mockResolvedValue({})
    mockApi.get.mockImplementation((url: string) => {
      if (url === '/projects') return Promise.resolve({ data: testProjects })
      if (url === '/projects/1/items?status=all') return Promise.resolve({ data: testItems })
      return Promise.resolve({ data: [] })
    })
    const store = useProjectsStore()
    store.selectedProjectId = 1

    await store.removeFromProject(10, 2)

    expect(mockApi.delete).toHaveBeenCalledWith('/project-items/10/projects/2')
  })

  // --- searchItems ---

  it('searches items across all projects', async () => {
    const results = [testItems[0]!, testItems[1]!]
    mockApi.get.mockResolvedValue({ data: results })
    const store = useProjectsStore()

    const result = await store.searchItems('budget')

    expect(mockApi.get).toHaveBeenCalledWith('/project-items/search', { params: { q: 'budget' }, holdsTypedInput: true })
    expect(result).toHaveLength(2)
  })

  // --- Project status ---

  it('sends the chosen filter and refetches when it changes', async () => {
    mockApi.get.mockResolvedValue({ data: testProjects })
    const store = useProjectsStore()

    await store.setStatusFilter('all')

    expect(store.statusFilter).toBe('all')
    expect(mockApi.get).toHaveBeenCalledWith('/projects', { params: { status: 'all' } })
  })

  it('does not refetch when the filter is already what was asked for', async () => {
    const store = useProjectsStore()

    await store.setStatusFilter('active')

    expect(mockApi.get).not.toHaveBeenCalled()
  })

  it('forgets a selected project the new filter no longer shows', async () => {
    mockApi.get.mockResolvedValue({ data: [] })
    const store = useProjectsStore()
    store.projects = [...testProjects] as never
    store.selectedProjectId = 1 as never
    store.selectedProjectIds = [1] as never

    await store.setStatusFilter('completed')

    // A selection pointing at an absent project renders an empty pane with no
    // way back to it.
    expect(store.selectedProjectId).toBeNull()
    expect(store.selectedProjectIds).toEqual([])
  })

  it('completes a project through PATCH rather than an action endpoint', async () => {
    mockApi.patch.mockResolvedValue({ data: {} })
    mockApi.get.mockResolvedValue({ data: testProjects })
    const store = useProjectsStore()

    await store.setProjectStatus(1 as never, 'completed')

    expect(mockApi.patch).toHaveBeenCalledWith('/projects/1', { status: 'completed' })
  })

  it('sends the reason when dropping', async () => {
    mockApi.patch.mockResolvedValue({ data: {} })
    mockApi.get.mockResolvedValue({ data: testProjects })
    const store = useProjectsStore()

    await store.setProjectStatus(1 as never, 'dropped', 'Go covers it')

    expect(mockApi.patch).toHaveBeenCalledWith('/projects/1', {
      status: 'dropped',
      status_reason: 'Go covers it',
    })
  })

  it('clears the selection when the closed project drops out of the list', async () => {
    mockApi.patch.mockResolvedValue({ data: {} })
    mockApi.get.mockResolvedValue({ data: testProjects.slice(1) })
    const store = useProjectsStore()
    store.selectedProjectId = 1 as never
    store.selectedProjectIds = [1] as never

    await store.setProjectStatus(1 as never, 'completed')

    expect(store.selectedProjectId).toBeNull()
  })

  it('keeps the selection when the filter still shows the closed project', async () => {
    mockApi.patch.mockResolvedValue({ data: {} })
    mockApi.get.mockResolvedValue({ data: testProjects })
    const store = useProjectsStore()
    store.statusFilter = 'all'
    store.selectedProjectId = 1 as never
    store.selectedProjectIds = [1] as never

    await store.setProjectStatus(1 as never, 'completed')

    expect(store.selectedProjectId).toBe(1)
  })
})
