// The navigation map. One list drives the sidebar, the mobile bar, the router
// and the command palette, so a screen cannot appear in one and be missing in
// another.

import type { MessageKey } from './i18n'

export type NavGroup = 'workspace' | 'manage' | 'personal'

export type NavIconName =
  | 'overview'
  | 'inbox'
  | 'list'
  | 'activity'
  | 'rules'
  | 'policies'
  | 'channels'
  | 'integrations'
  | 'analytics'
  | 'preferences'
  | 'users'
  | 'settings'
  | 'help'

export interface NavItem {
  id: string
  path: string
  group: NavGroup
  label: MessageKey
  icon: NavIconName
  /** Milestone that delivers the screen. Absent means it is built. */
  plannedIn?: number
  /** Shown in the mobile bottom bar. */
  primary?: boolean
}

export const NAV_GROUPS: { id: NavGroup; label: MessageKey }[] = [
  { id: 'workspace', label: 'nav.group.workspace' },
  { id: 'manage', label: 'nav.group.manage' },
  { id: 'personal', label: 'nav.group.personal' },
]

export const NAV_ITEMS: NavItem[] = [
  { id: 'overview', path: '/overview', group: 'workspace', label: 'nav.overview', icon: 'overview', primary: true },
  { id: 'inbox', path: '/inbox', group: 'workspace', label: 'nav.inbox', icon: 'inbox', primary: true },
  { id: 'all', path: '/all', group: 'workspace', label: 'nav.all', icon: 'list' },
  { id: 'activity', path: '/activity', group: 'workspace', label: 'nav.activity', icon: 'activity', primary: true },
  { id: 'rules', path: '/rules', group: 'manage', label: 'nav.rules', icon: 'rules' },
  { id: 'policies', path: '/policies', group: 'manage', label: 'nav.policies', icon: 'policies' },
  { id: 'channels', path: '/channels', group: 'manage', label: 'nav.channels', icon: 'channels' },
  { id: 'integrations', path: '/integrations', group: 'manage', label: 'nav.integrations', icon: 'integrations' },
  { id: 'users', path: '/users', group: 'manage', label: 'nav.users', icon: 'users' },
  { id: 'analytics', path: '/analytics', group: 'manage', label: 'nav.analytics', icon: 'analytics' },
  { id: 'preferences', path: '/preferences', group: 'personal', label: 'nav.preferences', icon: 'preferences' },
  { id: 'settings', path: '/settings', group: 'personal', label: 'nav.settings', icon: 'settings', primary: true },
  { id: 'help', path: '/help', group: 'personal', label: 'nav.help', icon: 'help' },
]

/** The screen shown at "/". */
export const HOME_PATH = '/inbox'

export function navByPath(path: string): NavItem | undefined {
  return NAV_ITEMS.find((item) => item.path === path)
}
