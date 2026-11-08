import { afterEach, describe, expect, it, vi } from 'vitest'
import { AxiosError, AxiosHeaders, type InternalAxiosRequestConfig } from 'axios'

type Page = (typeof import('../client'))['api']

// A page is a fresh load of the module, so its one-login-a-page flag starts clear.
async function page(status: number, seen: InternalAxiosRequestConfig[] = []): Promise<Page> {
  vi.resetModules()
  const { api } = await import('../client')
  api.defaults.adapter = async (config) => {
    seen.push(config)
    const response = { status, statusText: '', data: { error: 'Unauthorized', message: 'no session' }, headers: {}, config }
    if (status < 400) return response
    throw new AxiosError('refused', 'ERR_BAD_REQUEST', config, null, response)
  }
  return api
}

function stubLocation() {
  const assign = vi.fn()
  vi.stubGlobal('location', { pathname: '/', search: '?q=1', hash: '', assign })
  return assign
}

afterEach(() => {
  vi.unstubAllGlobals()
  sessionStorage.clear()
})

describe('api', () => {
  it('sends the header the API requires beside its session cookie', async () => {
    const seen: InternalAxiosRequestConfig[] = []
    const api = await page(200, seen)
    await api.get('/projects')
    await api.post('/projects', { name: 'x' })
    for (const config of seen) expect(AxiosHeaders.from(config.headers).get('X-CSRF')).toBe('1')
  })

  // A page's reads are one login however many get a 401. A person starting again
  // from a refused login is the second page. A third inside the minute is a login
  // that completes without taking, which would otherwise loop through the provider.
  it('sends the browser to log in once a page, from two pages a minute at most', async () => {
    const assign = stubLocation()

    const first = await page(401)
    void first.get('/projects')
    void first.get('/project-items')
    await vi.waitFor(() => expect(assign).toHaveBeenCalledTimes(1))
    expect(assign).toHaveBeenCalledWith('/api/auth/login?return_to=%2F%3Fq%3D1')

    const second = await page(401)
    void second.get('/projects')
    await vi.waitFor(() => expect(assign).toHaveBeenCalledTimes(2))

    const third = await page(401)
    await expect(third.get('/projects')).rejects.toMatchObject({ status: 401 })
    expect(assign).toHaveBeenCalledTimes(2)
  })

  it('fails a write, and a read of typed input, in place on a 401', async () => {
    const assign = stubLocation()
    const api = await page(401)
    const { extractApiError } = await import('../errors')

    // The page reads every failure through extractApiError, so the reason has to survive it.
    const refused = await api.patch('/project-items/1', { title: 'kept' }).catch((e: unknown) => e)
    expect(extractApiError(refused)).toMatchObject({ status: 401, detail: expect.stringMatching(/not saved/) })
    await expect(api.get('/project-items/search', { params: { q: 'typed' }, holdsTypedInput: true })).rejects.toMatchObject({ status: 401 })
    expect(assign).not.toHaveBeenCalled()
  })
})
