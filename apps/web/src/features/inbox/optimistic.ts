// What the list looks like the moment a person acts, before the service answers.
// If the service refuses, the caller puts the old page back.

import type { Action, InboxItem, InboxPage } from '../../lib/inbox'
import type { InboxFilters } from '../../lib/inboxQuery'

function changeItem(item: InboxItem, action: Action, now: string): InboxItem {
  switch (action.type) {
    case 'read':
      return { ...item, read: true, readAt: item.readAt ?? now }
    case 'unread':
      return { ...item, read: false, readAt: undefined }
    case 'acknowledge':
      return { ...item, read: true, acknowledgedAt: now }
    case 'resolve':
      return { ...item, read: true, resolvedAt: now }
    case 'snooze':
      return { ...item, snoozedUntil: new Date(Date.parse(now) + (action.minutes ?? 0) * 60_000).toISOString() }
    case 'priority':
      // Critical events keep their priority; clearing an override is left to the service's answer.
      return item.attentionClass === 'critical' || !action.priority || action.priority === 'critical'
        ? item
        : { ...item, priority: action.priority }
    default:
      return item
  }
}

/** Whether the item still belongs in the current view after the action. */
function stays(item: InboxItem, action: Action, filters: InboxFilters): boolean {
  switch (action.type) {
    case 'delete':
      return false
    case 'archive':
      return filters.archived
    case 'unarchive':
      return !filters.archived
    case 'read':
    case 'acknowledge':
    case 'resolve':
      return !(filters.unread && item.read)
    default:
      return true
  }
}

/** Applies an action to the selected ids of a page. Counts are left for the next reload. */
export function applyOptimistic(page: InboxPage, ids: string[], action: Action, filters: InboxFilters, now = new Date()): InboxPage {
  const chosen = new Set(ids)
  const stamp = now.toISOString()
  const items: InboxItem[] = []
  for (const item of page.items) {
    if (!chosen.has(item.id)) {
      items.push(item)
      continue
    }
    const next = changeItem(item, action, stamp)
    if (stays(next, action, filters)) items.push(next)
  }
  return { ...page, items, total: Math.max(0, page.total - (page.items.length - items.length)) }
}
