import { useEffect, useRef, useState } from 'react'
import type { Settings } from '../lib/api'
import { validateBackendUrl } from '../lib/settings'

interface SettingsDialogProps {
  open: boolean
  settings: Settings
  onSave: (settings: Settings) => void
  onClose: () => void
}

export function SettingsDialog({ open, settings, onSave, onClose }: SettingsDialogProps) {
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [backendUrl, setBackendUrl] = useState(settings.backendUrl)
  const [apiKey, setApiKey] = useState(settings.apiKey)
  const [urlError, setUrlError] = useState<string | null>(null)

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (open && !dialog.open) {
      setBackendUrl(settings.backendUrl)
      setApiKey(settings.apiKey)
      setUrlError(null)
      dialog.showModal()
    }
    if (!open && dialog.open) dialog.close()
  }, [open, settings])

  return (
    <dialog ref={dialogRef} className="dialog" aria-labelledby="settings-heading" onClose={onClose}>
      <form
        method="dialog"
        onSubmit={(e) => {
          e.preventDefault()
          const problem = validateBackendUrl(backendUrl)
          if (problem) {
            setUrlError(problem)
            return
          }
          onSave({ backendUrl: backendUrl.trim(), apiKey: apiKey.trim() })
        }}
      >
        <h2 id="settings-heading" className="dialog-title">
          Connection settings
        </h2>

        <label htmlFor="backend-url" className="field-label">
          Service address
        </label>
        <input
          id="backend-url"
          className="field"
          type="url"
          value={backendUrl}
          onChange={(e) => setBackendUrl(e.target.value)}
          aria-invalid={urlError ? 'true' : undefined}
          aria-describedby={urlError ? 'backend-url-error' : undefined}
          autoComplete="off"
        />
        {urlError && (
          <p id="backend-url-error" className="error-text">
            {urlError}
          </p>
        )}

        <label htmlFor="api-key" className="field-label">
          API key
        </label>
        <input
          id="api-key"
          className="field"
          type="password"
          value={apiKey}
          onChange={(e) => setApiKey(e.target.value)}
          autoComplete="off"
          aria-describedby="api-key-help"
        />
        <p id="api-key-help" className="hint">
          Use the value set as API_KEY on the service. It is kept in this browser tab only and forgotten when you close
          the tab.
        </p>

        <div className="actions">
          <button type="submit" className="button button-primary">
            Save and connect
          </button>
          <button type="button" className="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </dialog>
  )
}
