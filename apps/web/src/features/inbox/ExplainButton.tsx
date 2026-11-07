import { useEffect, useMemo, useState } from 'react'
import { useApp } from '../../app/AppState'
import { digestsClientFor, type Explanation } from '../../lib/digests'
import { SourceBadge } from '../../components/SourceBadge'
import { useI18n } from '../../lib/i18n/I18nContext'

/** "Explain in words": asks the service to say, in plain language, why this event was handled as it was. */
export function ExplainButton({ eventId }: { eventId: string }) {
  const { t } = useI18n()
  const { settings } = useApp()
  const client = useMemo(() => digestsClientFor(settings), [settings])
  const [text, setText] = useState<Explanation | null>(null)
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState(false)

  // A different notification means a fresh question.
  useEffect(() => {
    setText(null)
    setFailed(false)
  }, [eventId])

  const ask = async () => {
    setBusy(true)
    setFailed(false)
    try {
      setText(await client.explain(eventId))
    } catch {
      setFailed(true)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div role="status">
      {text === null ? (
        <button type="button" className="text-button" disabled={busy} onClick={() => void ask()}>
          {busy ? t('dg.explaining') : t('dg.explain')}
        </button>
      ) : (
        <>
          <p className="prose">{text.text}</p>
          <SourceBadge source={text.source} model={text.model} />
        </>
      )}
      {failed && <p className="error-text">{t('dg.explainFailed')}</p>}
    </div>
  )
}
