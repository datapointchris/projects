import { describe, expect, it } from 'vitest'
import projectSchema from '../../../../api/schemas/project.schema.json'
import { DEFAULT_PROJECT_KIND, PROJECT_KINDS, PROJECT_STATUSES } from '../types'

// The modal offers these kinds and the filter these statuses, so a kind or status
// the API gains or loses fails here before the page offers the wrong set.
describe('the project vocabulary', () => {
  it('lists what the API schema lists', () => {
    const { kind, status } = projectSchema.properties
    expect([...PROJECT_KINDS]).toEqual(kind.enum)
    expect(DEFAULT_PROJECT_KIND).toBe(kind.default)
    expect([...PROJECT_STATUSES]).toEqual(status.enum)
  })
})
