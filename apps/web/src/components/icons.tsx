// Small inline icons. They are decorative: the button or label next to them
// carries the accessible name.

const base = {
  width: 20,
  height: 20,
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 2,
  strokeLinecap: 'round',
  strokeLinejoin: 'round',
  'aria-hidden': true,
  focusable: false,
} as const

export function BellIcon() {
  return (
    <svg {...base}>
      <path d="M6 9a6 6 0 1 1 12 0c0 6 2 7.5 2 7.5H4S6 15 6 9Z" />
      <path d="M10 20a2 2 0 0 0 4 0" />
    </svg>
  )
}

export function SearchIcon() {
  return (
    <svg {...base}>
      <circle cx="11" cy="11" r="6.5" />
      <path d="m20 20-4.2-4.2" />
    </svg>
  )
}

export function MenuIcon() {
  return (
    <svg {...base}>
      <path d="M4 7h16M4 12h16M4 17h16" />
    </svg>
  )
}

export function CloseIcon() {
  return (
    <svg {...base}>
      <path d="m6 6 12 12M18 6 6 18" />
    </svg>
  )
}

export function ChevronIcon({ direction = 'down' }: { direction?: 'down' | 'left' | 'right' }) {
  const rotate = direction === 'down' ? 0 : direction === 'left' ? 90 : -90
  return (
    <svg {...base} style={{ transform: `rotate(${rotate}deg)` }}>
      <path d="m6 9 6 6 6-6" />
    </svg>
  )
}

export function UserIcon() {
  return (
    <svg {...base}>
      <circle cx="12" cy="8" r="4" />
      <path d="M4 21a8 8 0 0 1 16 0" />
    </svg>
  )
}

export function CommandIcon() {
  return (
    <svg {...base}>
      <path d="M9 6a3 3 0 1 0-3 3h12a3 3 0 1 0-3-3v12a3 3 0 1 0 3-3H6a3 3 0 1 0 3 3Z" />
    </svg>
  )
}

const NAV_PATHS: Record<string, string> = {
  overview: 'M4 4h7v7H4zM13 4h7v4h-7zM13 11h7v9h-7zM4 14h7v6H4z',
  inbox: 'M4 13V6a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v7M4 13l2.2 6h11.6L20 13M4 13h4.5a3.5 3.5 0 0 0 7 0H20',
  list: 'M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01',
  activity: 'M3 12h4l3-8 4 16 3-8h4',
  digests: 'M4 6h16v12H4zM4 9l8 5 8-5',
  rules: 'M4 6h10M18 6h2M4 12h2M10 12h10M4 18h12M20 18h0M14 4v4M8 10v4M16 16v4',
  policies: 'M12 3 5 6v5c0 4.5 3 8 7 10 4-2 7-5.5 7-10V6Zm-3 9 2 2 4-4',
  channels: 'M4 5h16v10H9l-5 4Z',
  integrations: 'M9 7V3M15 7V3M7 7h10v4a5 5 0 0 1-10 0ZM12 16v5',
  analytics: 'M5 20V10M12 20V4M19 20v-7',
  users: 'M16 8a4 4 0 1 1-8 0 4 4 0 0 1 8 0ZM4 20c0-3.500 3.500-6 8-6s8 2.500 8 6',
  preferences: 'M4 7h9M17 7h3M4 17h3M11 17h9M15 5v4M9 15v4',
  settings:
    'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Zm7.4-3a7.4 7.4 0 0 0-.1-1.3l2-1.5-2-3.4-2.3.9a7.5 7.5 0 0 0-2.2-1.3L14.4 3h-4l-.4 2.4a7.5 7.5 0 0 0-2.2 1.3l-2.3-.9-2 3.4 2 1.5a7.400 7.400 0 0 0 0 2.600l-2 1.500 2 3.400 2.300-.9a7.500 7.500 0 0 0 2.200 1.300l.4 2.400h4l.4-2.400a7.500 7.500 0 0 0 2.200-1.300l2.300.9 2-3.400-2-1.500c.1-.4.1-.9.1-1.300Z',
  help: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18ZM9.500 9.500a2.500 2.500 0 1 1 3.500 2.300c-.7.400-1 .9-1 1.700M12 17h.01',
}

/** Icon for a navigation entry; the label next to it carries the accessible name. */
export function NavIcon({ name }: { name: string }) {
  return (
    <svg {...base}>
      <path d={NAV_PATHS[name] ?? NAV_PATHS.list} />
    </svg>
  )
}
