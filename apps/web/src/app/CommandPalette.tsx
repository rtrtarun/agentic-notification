import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useToast } from '../design/Toast'
import { fuzzyFilter } from '../lib/fuzzy'
import { LOCALES } from '../lib/i18n'
import { useI18n } from '../lib/i18n/I18nContext'
import { NAV_ITEMS } from '../lib/nav'
import { THEMES, changeTheme } from '../lib/theme'
import { useApp } from './AppState'
import { SEARCH_ID } from './AppTopBar'

interface Command {
  id: string
  label: string
  group: 'go' | 'actions'
  run: () => void
}

/** Ctrl/Cmd+K. Fuzzy search over screens and actions, driven entirely from the keyboard. */
export function CommandPalette({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t, setLocale } = useI18n()
  const { markEverythingRead, openSettings } = useApp()
  const toast = useToast()
  const navigate = useNavigate()
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const returnFocus = useRef<HTMLElement | null>(null)

  const commands = useMemo<Command[]>(() => {
    const go: Command[] = NAV_ITEMS.map((item) => ({
      id: `go-${item.id}`,
      label: `${t('palette.go')} ${t(item.label)}`,
      group: 'go',
      run: () => navigate(item.path),
    }))
    const actions: Command[] = [
      {
        id: 'mark-all',
        label: t('cmd.markAll'),
        group: 'actions',
        run: () => {
          markEverythingRead()
            .then(() => toast.push(t('toast.allRead'), 'success'))
            .catch(() => toast.push(t('inbox.failed', { message: '' }), 'error'))
        },
      },
      {
        id: 'search',
        label: t('cmd.search'),
        group: 'actions',
        run: () => {
          navigate('/inbox')
          window.setTimeout(() => document.getElementById(SEARCH_ID)?.focus(), 0)
        },
      },
      { id: 'connection', label: t('cmd.connection'), group: 'actions', run: openSettings },
      ...THEMES.map<Command>((th) => ({
        id: `theme-${th.id}`,
        label: t('cmd.theme', { name: t(`theme.${th.id}`) }),
        group: 'actions',
        run: () => {
          changeTheme(th.id)
          toast.push(t('toast.themeChanged', { name: t(`theme.${th.id}`) }))
        },
      })),
      ...LOCALES.map<Command>((l) => ({
        id: `lang-${l.code}`,
        label: t('cmd.language', { name: l.name }),
        group: 'actions',
        run: () => setLocale(l.code),
      })),
    ]
    return [...actions, ...go]
  }, [t, navigate, markEverythingRead, openSettings, toast, setLocale])

  const results = useMemo(() => fuzzyFilter(commands, query, (c) => c.label), [commands, query])

  // Open: remember where focus was, start clean. Close: give focus back.
  useEffect(() => {
    if (open) {
      returnFocus.current = document.activeElement as HTMLElement | null
      setQuery('')
      setActive(0)
      inputRef.current?.focus()
    } else {
      returnFocus.current?.focus()
    }
  }, [open])

  if (!open) return null

  const run = (command: Command | undefined) => {
    if (!command) return
    onClose()
    command.run()
  }

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActive((i) => Math.min(i + 1, results.length - 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActive((i) => Math.max(i - 1, 0))
    } else if (e.key === 'Enter') {
      e.preventDefault()
      run(results[active])
    } else if (e.key === 'Escape') {
      onClose()
    } else if (e.key === 'Tab') {
      e.preventDefault() // the palette is one control; keep focus in the input
    }
  }

  return (
    <div className="palette-layer">
      <button type="button" className="scrim" aria-label={t('nav.closeMenu')} tabIndex={-1} onClick={onClose} />
      <div className="palette" role="dialog" aria-modal="true" aria-label={t('palette.title')} onKeyDown={onKeyDown}>
        <input
          ref={inputRef}
          className="palette-input"
          type="text"
          role="combobox"
          aria-expanded="true"
          aria-controls="palette-list"
          aria-activedescendant={results[active] ? `palette-${results[active].id}` : undefined}
          placeholder={t('palette.placeholder')}
          value={query}
          autoComplete="off"
          onChange={(e) => {
            setQuery(e.target.value)
            setActive(0)
          }}
        />
        <ul className="palette-list" id="palette-list" role="listbox" aria-label={t('palette.title')}>
          {results.map((c, i) => (
            <li
              key={c.id}
              id={`palette-${c.id}`}
              role="option"
              aria-selected={i === active}
              className="palette-item"
              ref={(el) => {
                if (el && i === active) el.scrollIntoView?.({ block: 'nearest' })
              }}
              onPointerMove={() => setActive(i)}
              onClick={() => run(c)}
            >
              <span>{c.label}</span>
              <span className="palette-group">{c.group === 'go' ? t('palette.go') : t('palette.actions')}</span>
            </li>
          ))}
          {results.length === 0 && <li className="palette-empty">{t('palette.empty')}</li>}
        </ul>
        <p className="palette-hint">{t('palette.hint')}</p>
      </div>
    </div>
  )
}
