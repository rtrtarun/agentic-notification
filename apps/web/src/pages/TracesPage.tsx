import { useEffect, useMemo, useState } from 'react'
import { useApp } from '../app/AppState'
import { LoadGate, useLoad } from '../features/manage/useLoad'
import { classLabel, eventLabel } from '../lib/events'
import { useI18n } from '../lib/i18n/I18nContext'
import { barWidths, formatCost, formatDuration, tracesClientFor, type TraceDetail, type TraceStep, type TraceSummary } from '../lib/traces'

/** AgentPulse: each run of an AI agent as an ordered timeline, with what it cost and what the platform decided about every step. */
export default function TracesPage() {
  const { t } = useI18n()
  const { settings } = useApp()
  const client = useMemo(() => tracesClientFor(settings), [settings])
  const { state, reload } = useLoad((signal) => client.list(signal), [client], 30_000)
  const [selected, setSelected] = useState<string | null>(null)

  // Open the most recent run by default.
  useEffect(() => {
    if (selected === null && state.data && state.data.length > 0) setSelected(state.data[0].traceId)
  }, [state.data, selected])

  return (
    <section className="mpage" aria-labelledby="tr-title">
      <div className="mpage-head">
        <div>
          <h1 className="page-title" id="tr-title">
            {t('tr.title')}
          </h1>
          <p className="mpage-lede">{t('tr.lede')}</p>
        </div>
      </div>
      <LoadGate state={state} onRetry={reload}>
        {(runs) =>
          runs.length === 0 ? (
            <p className="form-help">{t('tr.none')}</p>
          ) : (
            <div className="traces-layout">
              <nav aria-label={t('tr.runs')}>
                <ul className="item-list">
                  {runs.map((run) => (
                    <li key={run.traceId}>
                      <RunCard run={run} active={run.traceId === selected} onSelect={() => setSelected(run.traceId)} />
                    </li>
                  ))}
                </ul>
              </nav>
              <Timeline traceId={selected} />
            </div>
          )
        }
      </LoadGate>
      <p className="form-help">{t('tr.criticalNote')}</p>
    </section>
  )
}

function RunCard({ run, active, onSelect }: { run: TraceSummary; active: boolean; onSelect: () => void }) {
  const { t } = useI18n()
  return (
    <button type="button" className="item-card trace-card" aria-pressed={active} onClick={onSelect}>
      <strong>{run.agentName ?? run.traceId}</strong>
      <span className="trace-id">{run.traceId}</span>
      <span className="trace-status" data-status={run.status}>
        {t(`tr.status.${run.status}`)}
      </span>
      <span className="form-help">
        {t('tr.steps', { n: run.steps })}
        {run.retries > 0 && ` · ${t('tr.retries', { n: run.retries })}`}
        {run.failures > 0 && ` · ${t('tr.failures', { n: run.failures })}`}
      </span>
      <span className="form-help">
        {t('tr.duration', { time: formatDuration(run.durationMs) })} · {t('tr.cost', { cost: formatCost(run.costUsd) })}
      </span>
    </button>
  )
}

function Timeline({ traceId }: { traceId: string | null }) {
  const { t } = useI18n()
  const { settings, selectEvent } = useApp()
  const client = useMemo(() => tracesClientFor(settings), [settings])
  const { state, reload } = useLoad<TraceDetail | null>((signal) => (traceId ? client.get(traceId, signal) : Promise.resolve(null)), [client, traceId])
  if (traceId === null) return <p className="form-help">{t('tr.pick')}</p>
  return (
    <div>
      <LoadGate state={state} onRetry={reload}>
        {(run) => (run ? <RunDetail run={run} onInspect={selectEvent} /> : <p className="form-help">{t('tr.pick')}</p>)}
      </LoadGate>
    </div>
  )
}

function RunDetail({ run, onInspect }: { run: TraceDetail; onInspect: (eventId: string) => void }) {
  const { t } = useI18n()
  const widths = barWidths(run.stepList)
  return (
    <div className="trace-detail">
      <h2 className="sheet-subtitle">{t('tr.timeline', { agent: run.agentName ?? run.traceId })}</h2>
      <p className="form-help">
        {t('tr.tokens', { in: run.tokensIn.toLocaleString(), out: run.tokensOut.toLocaleString() })} · {t('tr.cost', { cost: formatCost(run.costUsd) })} ·{' '}
        {t('tr.worst', { class: classLabel(run.worstClass, t) })}
      </p>
      <ol className="trace-steps">
        {run.stepList.map((step, i) => (
          <StepRow key={step.eventId} step={step} width={widths[i]} onInspect={() => onInspect(step.eventId)} />
        ))}
      </ol>
    </div>
  )
}

function StepRow({ step, width, onInspect }: { step: TraceStep; width: number; onInspect: () => void }) {
  const { t } = useI18n()
  const name = eventLabel(step.eventType, t)
  const failed = /(\.|_)(failed|failure|error)$/.test(step.eventType)
  return (
    <li className="trace-step" data-kind={step.stepKind} data-failed={failed || undefined} style={{ paddingInlineStart: `${Math.min(step.depth, 6) * 0.75}rem` }}>
      <div className="trace-step-head">
        <strong>{name}</strong>
        {step.stepKind && <span className="chip">{t(`tr.kind.${step.stepKind}`)}</span>}
        {step.stepKind === 'retry' && step.attempt !== undefined && <span className="badge-pill" data-level="low">{t('tr.retry', { n: step.attempt })}</span>}
        {step.stepKind !== 'retry' && step.attempt !== undefined && step.attempt > 1 && <span className="form-help">{t('tr.attempt', { n: step.attempt })}</span>}
        <span className="badge-pill" data-level={step.attentionClass === 'critical' ? 'critical' : step.attentionClass === 'background' ? 'low' : 'normal'}>
          {classLabel(step.attentionClass, t)}
        </span>
      </div>
      {width > 0 && (
        <div className="trace-bar" role="img" aria-label={formatDuration(step.durationMs ?? 0)}>
          <span style={{ width: `${width}%` }} />
          <small>{formatDuration(step.durationMs ?? 0)}</small>
        </div>
      )}
      <p className="form-help">
        {step.toolName && `${step.toolName} · `}
        {(step.tokensIn ?? 0) + (step.tokensOut ?? 0) > 0 && `${t('tr.tokens', { in: (step.tokensIn ?? 0).toLocaleString(), out: (step.tokensOut ?? 0).toLocaleString() })} · `}
        {(step.costUsd ?? 0) > 0 && `${formatCost(step.costUsd ?? 0)} · `}
        {step.confidence !== undefined && `${t('tr.confidence', { pct: Math.round(step.confidence * 100) })} · `}
        {step.decisionReason}
      </p>
      <button type="button" className="text-button" aria-label={t('tr.inspectLabel', { name })} onClick={onInspect}>
        {t('tr.inspect')}
      </button>
    </li>
  )
}
