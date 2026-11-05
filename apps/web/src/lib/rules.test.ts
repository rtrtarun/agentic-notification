import { describe, expect, it } from 'vitest'
import { EMPTY_DRAFT, describeWhen, draftOf, draftProblem, evaluate, makeDemoRules, type Rule, type RuleDraft } from './rules'

const rule = (over: Partial<Rule>): Rule => ({
  id: over.id ?? over.name ?? 'r',
  name: 'r',
  when: {},
  then: { attentionClass: 'standard', reason: 'x' },
  ...over,
})

describe('evaluate', () => {
  const list = [
    rule({ name: 'security', locked: true, when: { source: 'github', eventType: 'security_advisory' }, then: { attentionClass: 'critical', reason: 'sec' } }),
    rule({ name: 'acme', when: { repository: 'acme/*' }, then: { attentionClass: 'background', reason: 'acme' } }),
    rule({ name: 'off', disabled: true, when: { source: 'jira' }, then: { attentionClass: 'critical', reason: 'off' } }),
  ]

  it('picks the first enabled match and marks critical as bypassing AI', () => {
    const r = evaluate(list, { source: 'github', eventType: 'security_advisory', repository: 'acme/api' })
    expect(r).toMatchObject({ matched: true, matchedRule: 'security', attentionClass: 'critical', aiBypassed: true })
    expect(r.steps.map((s) => [s.name, s.matched, !!s.winner])).toEqual([
      ['security', true, true],
      ['acme', true, false],
      ['off', false, false],
    ])
  })

  it('uses * as a wildcard that does not cross a slash', () => {
    expect(evaluate(list, { source: 'x', eventType: 'y', repository: 'acme/api' }).matchedRule).toBe('acme')
    expect(evaluate(list, { source: 'x', eventType: 'y', repository: 'acme/api/v2' }).matched).toBe(false)
  })

  it('never matches a disabled rule and falls back to standard', () => {
    const r = evaluate(list, { source: 'jira', eventType: '', repository: '' })
    expect(r).toMatchObject({ matched: false, attentionClass: 'standard', aiBypassed: false })
  })

  it('treats regular expression characters in a pattern literally', () => {
    const r = evaluate([rule({ name: 'dots', when: { repository: 'a.b/c+d' } })], { source: '', eventType: '', repository: 'axb/c+d' })
    expect(r.matched).toBe(false)
  })
})

describe('draft helpers', () => {
  it('rejects empty, duplicate and overlong drafts but lets a rule keep its own name', () => {
    const others = [rule({ id: '1', name: 'Quiet' })]
    expect(draftProblem({ ...EMPTY_DRAFT, name: '  ' }, others)).toBe('name')
    expect(draftProblem({ ...EMPTY_DRAFT, name: 'quiet' }, others)).toBe('duplicate')
    expect(draftProblem({ ...EMPTY_DRAFT, name: 'quiet' }, others, '1')).toBeNull()
    expect(draftProblem({ ...EMPTY_DRAFT, name: 'ok', description: 'x'.repeat(301) }, others)).toBe('long')
  })

  it('round-trips a rule to a draft and describes its condition', () => {
    const r = rule({ name: 'n', when: { source: 'github', repository: 'a/b' }, description: 'd' })
    expect(draftOf(r)).toMatchObject({ name: 'n', description: 'd', when: { source: 'github', repository: 'a/b' } })
    expect(describeWhen(r.when)).toEqual(['github', 'a/b'])
    expect(describeWhen({})).toEqual([])
  })
})

describe('demo client guards', () => {
  const draft = (name: string): RuleDraft => ({ ...EMPTY_DRAFT, name, then: { attentionClass: 'background', reason: '' } })

  it('seeds locked critical rules that cannot be edited or deleted', async () => {
    const c = makeDemoRules()
    const locked = (await c.list()).filter((r) => r.locked)
    expect(locked.length).toBeGreaterThan(0)
    await expect(c.remove(locked[0].id)).rejects.toThrow(/locked/)
    await expect(c.update(locked[0].id, draft('x'))).rejects.toThrow(/locked/)
  })

  it('creates, edits and deletes an unlocked rule', async () => {
    const c = makeDemoRules()
    const created = await c.create(draft(' mine '))
    expect(created.name).toBe('mine')
    expect(created.then.reason).toBe('matched rule mine')
    await c.update(created.id, { ...draft('mine'), disabled: true })
    expect((await c.list()).find((r) => r.id === created.id)?.disabled).toBe(true)
    await c.remove(created.id)
    expect((await c.list()).some((r) => r.id === created.id)).toBe(false)
  })

  it('rejects duplicates and bad orders but allows a valid reorder', async () => {
    const c = makeDemoRules()
    await expect(c.create(draft('default-github'))).rejects.toThrow(/invalid/)
    const list = await c.list()
    const ids = list.map((r) => r.id)
    await expect(c.reorder(ids.slice(1))).rejects.toThrow(/every rule id/)
    const lockedLast = [...ids.slice(2), ids[0], ids[1]]
    await expect(c.reorder(lockedLast)).rejects.toThrow(/locked rules must stay/)
    const swapped = [ids[0], ids[1], ids[3], ids[2]]
    expect((await c.reorder(swapped)).map((r) => r.id)).toEqual(swapped)
  })

  it('reports policies from the current rules', async () => {
    const c = makeDemoRules()
    const p = await c.policies()
    expect(p.lockedRules.length).toBe(2)
    expect(p.classes.find((x) => x.class === 'critical')?.aiEligible).toBe(false)
  })
})
