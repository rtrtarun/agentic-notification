import { categoryLabel, categoryOf } from '../lib/categories'
import { useI18n } from '../lib/i18n/I18nContext'

/** A coloured label for the kind of event. Colour comes from the theme's category palette. */
export function Chip({ eventType }: { eventType: string }) {
  const { t } = useI18n()
  const category = categoryOf(eventType)
  return (
    <span className="chip" data-category={category}>
      {categoryLabel(category, t)}
    </span>
  )
}
