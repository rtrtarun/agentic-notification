/** Placeholder rows while a list loads. Decorative: the list's own busy state carries the meaning. */
export function SkeletonRows({ count = 6 }: { count?: number }) {
  return (
    <ul className="skeleton-list" aria-hidden="true">
      {Array.from({ length: count }, (_, i) => (
        <li key={i} className="skeleton-row">
          <span className="skeleton skeleton-dot" />
          <span className="skeleton-lines">
            <span className="skeleton skeleton-line" style={{ width: `${55 + ((i * 13) % 30)}%` }} />
            <span className="skeleton skeleton-line skeleton-short" />
          </span>
        </li>
      ))}
    </ul>
  )
}
