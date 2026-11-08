import axios from 'axios'
import { createLogger } from '@/utils/logger'
import { ApiError, extractApiError } from './errors'

const logger = createLogger('ApiClient')

// Same origin in every environment: Caddy, or Vite's proxy in development, sends
// /api to the API. The API logs the browser in itself, so the browser holds one
// HttpOnly cookie and this code never sees a token.
const BASE = '/api/v1'
const LOGIN = '/api/auth/login'
const LOGOUT = '/api/auth/logout'

// The API refuses its session cookie on a request without this header, which a
// page on another origin cannot add without a preflight the API never answers.
const CSRF_HEADER = 'X-CSRF'

// A login that completes while the API still answers 401, as with cookies
// blocked, would send the browser round the provider forever. A person starting
// again from a refused login is a second page sending the browser to log in
// inside the window, so a third is the loop, and its reads fail instead.
const LOGIN_WINDOW_MS = 60_000
const LOGIN_STARTS_ALLOWED = 2
const LOGIN_STARTS_KEY = 'loginStarts'

const SESSION_ENDED_ON_WRITE = 'Your login has ended, so this change was not saved. Reload the page to log in again.'
const SESSION_ENDED_ON_TYPED_READ = 'Your login has ended. Copy what you typed, then reload the page to log in again.'
const LOGIN_REFUSED = 'Logging in did not complete. Wait a minute, then reload the page.'

declare module 'axios' {
  interface AxiosRequestConfig {
    // A read whose parameters were typed in and are held nowhere else, such as a
    // search. A 401 fails it in place, as it does a write, because sending the
    // browser to log in would drop what was typed.
    holdsTypedInput?: boolean
  }
}

// One start per page: the reads a page fires together all get the 401.
let loggingIn = false

function recentLoginStarts(now: number): number[] {
  try {
    const starts = JSON.parse(sessionStorage.getItem(LOGIN_STARTS_KEY) ?? '[]') as number[]
    return starts.filter((at) => now - at < LOGIN_WINDOW_MS)
  } catch {
    return []
  }
}

function sendToLogin(): boolean {
  if (loggingIn) return true
  const now = Date.now()
  const recent = recentLoginStarts(now)
  if (recent.length >= LOGIN_STARTS_ALLOWED) return false
  sessionStorage.setItem(LOGIN_STARTS_KEY, JSON.stringify([...recent, now]))
  loggingIn = true
  const here = location.pathname + location.search + location.hash
  location.assign(`${LOGIN}?return_to=${encodeURIComponent(here)}`)
  return true
}

export const LOGIN_AGAIN_HREF = `${LOGIN}?return_to=${encodeURIComponent('/')}`

// Ends this browser's session at the API. The identity provider keeps its own,
// so a login after it may complete without asking for anything.
export async function logOut(): Promise<void> {
  await api.post(LOGOUT, undefined, { baseURL: '' })
}

function sessionEnded(detail: string, requestId?: string): ApiError {
  return new ApiError({ message: `API 401: ${detail}`, status: 401, detail, requestId })
}

// Track request metadata (start time, request ID) outside Axios's config object.
// WeakMap avoids type augmentation issues and cleans up automatically.
interface RequestMeta {
  startTime: number
  requestId: string
}
const requestMeta = new WeakMap<object, RequestMeta>()

export const api = axios.create({
  baseURL: BASE,
  headers: {
    'Content-Type': 'application/json',
    [CSRF_HEADER]: '1',
  },
  timeout: 30000,
})

// Request interceptor: inject X-Request-ID and log outgoing request
api.interceptors.request.use((config) => {
  const requestId = crypto.randomUUID().slice(0, 8)
  config.headers.set('X-Request-ID', requestId)

  requestMeta.set(config, { startTime: Date.now(), requestId })

  logger.debug('request_outgoing', {
    method: config.method?.toUpperCase(),
    url: `${config.baseURL}${config.url}`,
    request_id: requestId,
  })

  return config
})

// Response interceptor: log response, convert errors to ApiError, and answer a 401
api.interceptors.response.use(
  (response) => {
    const meta = requestMeta.get(response.config)
    const duration = meta ? Date.now() - meta.startTime : 0

    logger.info('request_completed', {
      method: response.config.method?.toUpperCase(),
      url: `${response.config.baseURL}${response.config.url}`,
      status: response.status,
      duration_ms: duration,
      request_id: meta?.requestId,
    })

    return response
  },
  (error) => {
    const meta = error.config ? requestMeta.get(error.config) : undefined
    const duration = meta ? Date.now() - meta.startTime : 0
    const apiError = extractApiError(error)

    if (apiError.isNetworkError) {
      logger.error('request_network_error', {
        method: error.config?.method?.toUpperCase(),
        url: error.config ? `${error.config.baseURL}${error.config.url}` : 'unknown',
        error: apiError.detail,
        duration_ms: duration,
        request_id: meta?.requestId,
      })
    } else {
      logger.error('request_failed', {
        method: error.config?.method?.toUpperCase(),
        url: error.config ? `${error.config.baseURL}${error.config.url}` : 'unknown',
        status: apiError.status,
        detail: apiError.detail,
        duration_ms: duration,
        request_id: apiError.requestId || meta?.requestId,
      })
    }

    if (apiError.status === 401) {
      const method = (error.config?.method ?? 'get').toUpperCase()
      // A write fails in place so its caller rolls back and says so; navigating would drop the change.
      if (method !== 'GET') return Promise.reject(sessionEnded(SESSION_ENDED_ON_WRITE, apiError.requestId))
      if (error.config?.holdsTypedInput) return Promise.reject(sessionEnded(SESSION_ENDED_ON_TYPED_READ, apiError.requestId))
      // The page is navigating away, so the caller never settles and shows no error.
      if (sendToLogin()) return new Promise(() => {})
      return Promise.reject(sessionEnded(LOGIN_REFUSED, apiError.requestId))
    }

    return Promise.reject(apiError)
  },
)

export type {
  Project,
  ProjectCreate,
  ProjectKind,
  ProjectStatus,
  ProjectStatusFilter,
  ProjectUpdate,
  ProjectWithItemCount,
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
} from './types'
