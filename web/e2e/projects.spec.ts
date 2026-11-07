import { test, expect, type Page } from '@playwright/test'

const SUCCESS = '.flash-messages__message--success'
const ERROR = '.flash-messages__message--error'

async function createProject(page: Page, name: string) {
  await page.getByTestId('project-add-button').click()
  await page.getByTestId('project-name-input').fill(name)
  await page.getByTestId('project-submit-button').click()
  await expect(page.locator(SUCCESS, { hasText: `${name} added` })).toBeVisible()
  await expect(page.getByTestId('project-name-input')).not.toBeVisible()
}

async function createItem(page: Page, title: string, notes: string) {
  await page.getByTestId('project-item-add-button').click()
  await page.getByTestId('project-item-title-input').fill(title)
  await page.getByTestId('project-item-notes-input').fill(notes)
  await page.getByTestId('project-item-submit-button').click()
  await expect(page.locator(SUCCESS, { hasText: `${title} added` })).toBeVisible()
  await expect(page.getByTestId('project-item-title-input')).not.toBeVisible()
}

// One roundtrip through the app's own stack: the stand-in provider logs the
// browser in, Caddy serves the page and proxies /api, and the API writes Postgres.
test('logs in, then creates, edits and deletes a project and its item', async ({ page }) => {
  const stamp = Date.now()
  const projectName = `smoke/${stamp}`
  const itemTitle = `Smoke item ${stamp}`

  // No session yet, so the page's first read sends the browser through the login and back.
  await page.goto('/')
  await expect(page).toHaveURL(/\/$/)
  await expect(page.locator('.projects-page')).toBeVisible()
  await expect(page.getByTestId('project-add-button')).toBeVisible()

  await createProject(page, projectName)
  const project = page.getByTestId('project-item').filter({ hasText: projectName })
  await expect(project).toBeVisible()

  // A name holding a slash reaches the API escaped, through Caddy, on the browser's own session.
  const byName = await page.request.get(`/api/v1/projects/${encodeURIComponent(projectName)}`, { headers: { 'X-CSRF': '1' } })
  expect(byName.status()).toBe(200)
  expect((await byName.json()).name).toBe(projectName)

  await project.click()
  await expect(page.locator('.projects-page__items-header')).toContainText(projectName)

  await createItem(page, itemTitle, 'Written by the smoke test')
  const row = page.getByTestId('project-item-row').filter({ hasText: itemTitle })
  await expect(row).toBeVisible()

  await row.getByTestId('project-item-edit-button').click()
  await page.getByTestId('project-item-title-input').fill(`${itemTitle} edited`)
  await page.getByTestId('project-item-submit-button').click()
  await expect(page.locator(SUCCESS, { hasText: 'updated' })).toBeVisible()
  const edited = page.getByTestId('project-item-row').filter({ hasText: `${itemTitle} edited` })
  await expect(edited).toBeVisible()

  // Reloading reads everything back from the API, so what shows is what was stored.
  await page.reload()
  await page.getByTestId('project-item').filter({ hasText: projectName }).click()
  await expect(page.getByTestId('project-item-row').filter({ hasText: `${itemTitle} edited` })).toBeVisible()

  await page
    .getByTestId('project-item-row')
    .filter({ hasText: `${itemTitle} edited` })
    .getByTestId('project-item-delete-button')
    .click()
  await expect(page.getByTestId('project-item-row').filter({ hasText: itemTitle })).toHaveCount(0)

  await page.getByTestId('project-delete-button').click()
  await expect(page.locator(SUCCESS, { hasText: `${projectName} deleted` })).toBeVisible()
  await expect(page.getByTestId('project-item').filter({ hasText: projectName })).toHaveCount(0)
  await expect(page.locator(ERROR)).toHaveCount(0)

  await page.getByTestId('logout-button').click()
  await expect(page.getByTestId('logged-out')).toBeVisible()
  const afterLogout = await page.request.get('/api/v1/projects', { headers: { 'X-CSRF': '1' } })
  expect(afterLogout.status()).toBe(401)
})
