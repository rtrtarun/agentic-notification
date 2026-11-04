import type { AttentionClass } from '../lib/api'
import { CLASS_LABELS } from '../lib/events'
import { Flag } from './Flag'

const MEANINGS: Record<AttentionClass, string> = {
  critical: 'Always reaches you right away.',
  standard: 'Held back so it does not interrupt you.',
  background: 'Stays quiet. Kept for reference.',
}

const ORDER: AttentionClass[] = ['critical', 'standard', 'background']

/** The three classes in plain words, so the shapes in the list are never a puzzle. */
export function Legend() {
  return (
    <ul className="legend" aria-label="How events are sorted">
      {ORDER.map((kind) => (
        <li key={kind} className="legend-item">
          <Flag kind={kind} />
          <span>
            <strong>{CLASS_LABELS[kind]}.</strong> {MEANINGS[kind]}
          </span>
        </li>
      ))}
    </ul>
  )
}
