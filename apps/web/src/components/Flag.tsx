import type { AttentionClass } from '../lib/api'

export type FlagKind = AttentionClass | 'merged'

const SHAPE_NAMES: Record<FlagKind, string> = {
  critical: 'Critical',
  standard: 'Standard',
  background: 'Background',
  merged: 'Merged',
}

/**
 * A signal flag. Colour and shape both carry the class, so it reads without
 * colour vision: a solid pennant is critical, a square flag is standard, a thin
 * outlined flag is background and a swallowtail is a merged event.
 */
export function Flag({ kind }: { kind: FlagKind }) {
  return <span className="flag" data-kind={kind} role="img" aria-label={SHAPE_NAMES[kind]} />
}
