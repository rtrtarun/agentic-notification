import { useState } from 'react'
import type { ApiError, Settings } from '../lib/api'

interface ErrorNoticeProps {
  error: ApiError
  settings: Settings
  onOpenSettings: () => void
  onRetry: () => void
}

/** Says what went wrong and what to do about it. Never apologises, never vague. */
export function ErrorNotice({ error, settings, onOpenSettings, onRetry }: ErrorNoticeProps) {
  switch (error.kind) {
    case 'unauthorized':
      return (
        <section className="notice" role="alert">
          <h2 className="notice-title">The service rejected this API key.</h2>
          <p className="prose">Enter the value that is set as API_KEY on the service.</p>
          <button type="button" className="button button-primary" onClick={onOpenSettings}>
            Enter API key
          </button>
        </section>
      )
    case 'unconfigured':
      return (
        <section className="notice" role="alert">
          <h2 className="notice-title">The service has no API key, so it refuses every request.</h2>
          <p className="prose">Set API_KEY on the service and restart it. Then enter the same key here.</p>
          <button type="button" className="button button-primary" onClick={onOpenSettings}>
            Enter API key
          </button>
        </section>
      )
    case 'unreachable':
      return (
        <section className="notice" role="alert">
          <h2 className="notice-title">Can&rsquo;t reach {settings.backendUrl}.</h2>
          <p className="prose">
            Check that the service is running. If it is, add {window.location.origin} to CORS_ALLOWED_ORIGINS on the
            service, or correct the address.
          </p>
          <div className="actions">
            <button type="button" className="button button-primary" onClick={onRetry}>
              Try again
            </button>
            <button type="button" className="button" onClick={onOpenSettings}>
              Change address
            </button>
          </div>
        </section>
      )
    default:
      return (
        <section className="notice" role="alert">
          <h2 className="notice-title">The service hit an error.</h2>
          <p className="prose">Events are not loading. Check the service logs for the request that failed, then try again.</p>
          <button type="button" className="button button-primary" onClick={onRetry}>
            Try again
          </button>
        </section>
      )
  }
}

/** Shown when the service is healthy but has not received an event yet. */
export function EmptyNotice({ settings }: { settings: Settings }) {
  const [copied, setCopied] = useState(false)
  const webhookUrl = `${settings.backendUrl.replace(/\/+$/, '')}/webhooks/github`

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(webhookUrl)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2500)
    } catch {
      setCopied(false)
    }
  }

  return (
    <section className="notice" aria-labelledby="empty-heading">
      <h2 id="empty-heading" className="notice-title">
        No events yet.
      </h2>
      <p className="prose">Send your first GitHub event to see how the system decides what to do with it.</p>
      <ol className="steps">
        <li>In your GitHub repository, open Settings, then Webhooks, then Add webhook.</li>
        <li>
          Set the payload URL to <span className="url">{webhookUrl}</span> and the content type to application/json.
        </li>
        <li>Set the secret to the value of GITHUB_WEBHOOK_SECRET on the service, then choose the events to send.</li>
      </ol>
      <button type="button" className="button" onClick={copy}>
        {copied ? 'Copied' : 'Copy webhook URL'}
      </button>
    </section>
  )
}
