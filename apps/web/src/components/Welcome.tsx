import { Legend } from './Legend'

interface WelcomeProps {
  onTryDemo: () => void
  onConnect: () => void
}

/** Shown on a first visit, before anything is connected. Two clear ways forward. */
export function Welcome({ onTryDemo, onConnect }: WelcomeProps) {
  return (
    <section className="welcome" aria-labelledby="welcome-heading">
      <h1 id="welcome-heading" className="welcome-title">
        Decide what deserves to interrupt you.
      </h1>
      <p className="lead-detail">
        Attention reads your events, sends only what matters and holds back the rest. This console shows what it
        decided and why, and lets you correct it.
      </p>

      <h2 className="sheet-subtitle">How it sorts events</h2>
      <Legend />

      <div className="actions welcome-actions">
        <button type="button" className="button button-primary" onClick={onTryDemo}>
          Try the demo
        </button>
        <button type="button" className="button" onClick={onConnect}>
          Connect your service
        </button>
      </div>
      <p className="hint">The demo uses sample data. Nothing is sent or saved.</p>
    </section>
  )
}
