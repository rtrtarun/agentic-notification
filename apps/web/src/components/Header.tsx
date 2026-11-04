import type { Feed } from '../hooks/useEvents'

interface HeaderProps {
  feed: Feed
  onOpenSettings: () => void
  /** True while a full-screen layer covers the page. */
  inert?: boolean
}

function connectionText(feed: Feed): { text: string; state: 'ok' | 'wait' | 'bad' } {
  if (feed.status === 'loading' && feed.events.length === 0) return { text: 'Connecting', state: 'wait' }
  if (feed.status === 'error') {
    return feed.error.kind === 'unreachable'
      ? { text: 'Not connected', state: 'bad' }
      : { text: 'Not signed in', state: 'bad' }
  }
  return { text: 'Connected', state: 'ok' }
}

export function Header({ feed, onOpenSettings, inert }: HeaderProps) {
  const { text, state } = connectionText(feed)
  return (
    <header className="masthead" {...(inert ? { inert: '' } : {})}>
      <p className="wordmark">Attention</p>
      <div className="masthead-actions">
        <p className="connection" data-state={state} role="status">
          <span className="connection-dot" aria-hidden="true" />
          {text}
        </p>
        <button type="button" className="text-button" onClick={onOpenSettings}>
          Connection settings
        </button>
      </div>
    </header>
  )
}
