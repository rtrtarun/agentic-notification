// Every event type belongs to a category, shown as a coloured chip. Colour tells
// you what kind of thing happened; the flag's shape tells you how much it matters.

import type { Translator } from './i18n'
import { tEn } from './i18n'

export type Category = 'security' | 'code' | 'issues' | 'build' | 'release' | 'social' | 'other'

const BY_EVENT: Record<string, Category> = {
  security_advisory: 'security',
  dependabot_alert: 'security',
  push: 'code',
  pull_request: 'code',
  pull_request_review: 'code',
  issues: 'issues',
  issue_comment: 'issues',
  workflow_run: 'build',
  check_run: 'build',
  release: 'release',
  star: 'social',
  watch: 'social',
  fork: 'social',
}

export function categoryOf(eventType: string): Category {
  return BY_EVENT[eventType] ?? 'other'
}

export function categoryLabel(category: Category, t: Translator = tEn): string {
  return t(`cat.${category}`)
}
