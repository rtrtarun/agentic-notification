import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useApp } from '../app/AppState'
import { useAuth } from '../app/AuthGate'
import { useToast } from '../design/Toast'
import { LoadGate, useLoad } from '../features/manage/useLoad'
import { ApiError } from '../lib/api'
import { SourceBadge } from '../components/SourceBadge'
import { describeEvery, digestsClientFor, sourceOf, type Digest } from '../lib/digests'
import { relativeTime } from '../lib/events'
import { useI18n } from '../lib/i18n/I18nContext'

/** What was held back, summarised: the digests Attention Intelligence wrote, newest first. */
export default function DigestsPage() {
  const { t, locale } = useI18n()
  const toast = useToast()
  const { settings, now, selectEvent } = useApp()
  const { isAdmin } = useAuth()
  const client = useMemo(() => digestsClientFor(settings), [settings])
  const { state, reload } = useLoad((signal) => client.list(signal), [client], 60_000)
  const [busy, setBusy] = useState(false)
  const [openId, setOpenId] = useState<string | null>(null)

  const generate = async () => {
    setBusy(true)
    try {
      const made = await client.generate()
      if (made) {
        setOpenId(made.id)
        toast.push(t('dg.made'), 'success')
      } else {
        toast.push(t('dg.nothing'), 'info')
      }
      reload()
    } catch (err) {
      toast.push(err instanceof ApiError ? err.message : t('dg.failed'), 'error')
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="mpage" aria-labelledby="dg-title">
      <div className="mpage-head">
        <div>
          <h1 className="page-title" id="dg-title">
            {t('nav.digests')}
          </h1>
          <p className="mpage-lede">{t('dg.lede')}</p>
        </div>
        {isAdmin && (
          <button type="button" className="button button-primary" disabled={busy} onClick={() => void generate()}>
            {busy ? t('dg.making') : t('dg.make')}
          </button>
        )}
      </div>
      <p className="panel" role="note">
        {t('dg.criticalNote')}
      </p>
      <LoadGate state={state} onRetry={reload}>
        {(data) => {
          const every = describeEvery(data.every)
          return (
            <>
              <p className="form-help">{every ? t(`dg.every.${every.unit}`, { n: every.n }) : t('dg.onRequest')}</p>
              {data.digests.length === 0 ? (
                <p className="panel ledger-empty">{t('dg.none')}</p>
              ) : (
                <ul className="card-list" aria-label={t('nav.digests')}>
                  {data.digests.map((d) => (
                    <DigestCard key={d.id} digest={d} open={openId === d.id} onToggle={() => setOpenId(openId === d.id ? null : d.id)} when={relativeTime(d.createdAt, now, locale, t)} onOpenEvent={selectEvent} />
                  ))}
                </ul>
              )}
              <p className="form-help">
                <Link to="/preferences">{t('dg.prefs')}</Link>
              </p>
            </>
          )
        }}
      </LoadGate>
    </section>
  )
}

function DigestCard({ digest: d, open, onToggle, when, onOpenEvent }: { digest: Digest; open: boolean; onToggle: () => void; when: string; onOpenEvent: (id: string) => void }) {
  const { t } = useI18n()
  return (
    <li className="item-card">
      <h3>{d.title}</h3>
      <p className="form-help">
        {when} · {d.createdBy === 'schedule' ? t('dg.byschedule') : t('dg.by', { who: d.createdBy })}
      </p>
      <p>{d.summary}</p>
      <SourceBadge source={sourceOf(d)} model={d.model} />
      {d.highlights.length > 0 && (
        <>
          <h4 className="sheet-subtitle">{t('dg.lookFirst')}</h4>
          <ul>
            {d.highlights.map((h) => (
              <li key={h.eventId}>
                <button type="button" className="row-link" onClick={() => onOpenEvent(h.eventId)}>
                  {h.title}
                </button>{' '}
                <span className="form-help">{h.reason}</span>
              </li>
            ))}
          </ul>
        </>
      )}
      <div className="form-actions">
        <button type="button" className="button" aria-expanded={open} onClick={onToggle}>
          {open ? t('dg.hide') : t('dg.show')}
        </button>
      </div>
      {open && (
        <dl className="kv">
          {d.groups.map((g) => (
            <div key={g.key} style={{ display: 'contents' }}>
              <dt>{g.title}</dt>
              <dd>{g.summary}</dd>
            </div>
          ))}
        </dl>
      )}
    </li>
  )
}
