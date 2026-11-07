// --- Project ---

/** Values come from the project_kinds lookup table; adding one is an insert plus this union. */
export type ProjectKind = 'build' | 'chore' | 'life'

/**
 * Where a project is in its lifecycle, from the project_statuses lookup table.
 * A project is a finite effort, so completing it is what hides it — there is no
 * separate archive flag the way items have one. `dropped` exists beside
 * `completed` because `completed` alone would force you to lie about anything
 * you merely stopped caring about, and it always carries a reason. `someday`
 * hides like a closed project, but it is open work set aside, so the server
 * leaves `closed_at` empty.
 */
export type ProjectStatus = 'active' | 'someday' | 'completed' | 'dropped'

/** `all` is the absence of the filter, not a status a project can be in. */
export type ProjectStatusFilter = ProjectStatus | 'all'

export interface Project {
  id: string
  name: string
  description?: string
  kind: ProjectKind
  status: ProjectStatus
  status_reason?: string | null
  closed_at?: string | null
  position: number
  created_at: string
}

export interface ProjectCreate {
  name: string
  description?: string
  kind?: ProjectKind
  position?: number
}

/**
 * `closed_at` is absent deliberately — the server stamps it on the transition
 * into a terminal status and clears it on reopen, so it cannot drift from the
 * status it describes.
 */
export interface ProjectUpdate {
  name?: string
  description?: string | null
  kind?: ProjectKind
  status?: ProjectStatus
  status_reason?: string | null
  position?: number
}

// open_count and completed_count exclude archived items, so they sum to
// item_count only when nothing in the project is archived.
export interface ProjectWithItemCount extends Project {
  item_count: number
  open_count: number
  completed_count: number
}

export interface ProjectItem {
  id: string
  // Server-assigned handle. `id` is the key and is never shown to anyone; this
  // is what a row displays and what a command line takes.
  number: number
  title: string
  notes?: string
  completed: boolean
  archived: boolean
  created_at: string
  updated_at: string
}

export interface ProjectItemCreate {
  title: string
  notes?: string
  project_ids: string[]
}

export interface ProjectItemUpdate {
  title?: string
  notes?: string | null
  completed?: boolean
  archived?: boolean
}

export interface ProjectItemDetail extends ProjectItem {
  projects: Project[]
  dependency_ids: string[]
}

export interface ProjectItemInProject extends ProjectItem {
  position: number
}

export interface ProjectItemReorder {
  project_id: string
  position: number
}

export interface ProjectItemMembershipCreate {
  project_id: string
}

export interface ProjectItemDependencyCreate {
  depends_on_id: string
}

// --- Project Item Task ---

export interface ProjectItemTask {
  id: string
  item_id: string
  title: string
  completed: boolean
  position: number
  created_at: string
}

export interface ProjectItemTaskCreate {
  title: string
  position?: number
}

export interface ProjectItemTaskUpdate {
  title?: string
  completed?: boolean
  position?: number
}
