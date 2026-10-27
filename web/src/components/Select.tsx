import { Children, isValidElement, useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import type { KeyboardEvent, ReactNode } from 'react'

type Option = { value: string; children: ReactNode; disabled?: boolean }

/** Option children describe choices; only the custom button and listbox render. */
export function Select({ id, value, onChange, disabled = false, children }: {
  id: string
  value: string
  onChange: (value: string) => void
  disabled?: boolean
  children: ReactNode
}) {
  const options = Children.toArray(children).filter(isValidElement).map((child) => child.props as Option)
  const listId = useId()
  const trigger = useRef<HTMLButtonElement>(null)
  const list = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(0)
  const search = useRef({ text: '', time: 0 })
  const selected = options.findIndex((o) => o.value === value)
  const enabled = options.map((o, i) => o.disabled ? -1 : i).filter((i) => i >= 0)
  const close = () => setOpen(false)
  const show = (index = selected >= 0 ? selected : enabled[0]) => {
    if (disabled || !enabled.length) return
    setActive(options[index]?.disabled ? enabled[0] : index)
    setOpen(true)
  }
  const choose = (index: number) => {
    if (!options[index] || options[index].disabled) return
    onChange(options[index].value)
    close()
    trigger.current?.focus()
  }
  useLayoutEffect(() => {
    if (!open || !list.current || !trigger.current) return
    const menu = list.current
    const rect = trigger.current.getBoundingClientRect()
    const viewport = window.visualViewport
    const top = viewport?.offsetTop ?? 0
    const bottom = top + (viewport?.height ?? innerHeight)
    const left = viewport?.offsetLeft ?? 0
    const width = Math.min(rect.width, (viewport?.width ?? innerWidth) - 16)
    const below = bottom - rect.bottom - 12
    const above = rect.top - top - 12
    const upward = below < 200 && above > below
    menu.style.width = `${width}px`
    menu.style.left = `${Math.max(left + 8, Math.min(rect.left, left + (viewport?.width ?? innerWidth) - width - 8))}px`
    menu.style.maxHeight = `${Math.max(44, Math.min(280, upward ? above : below))}px`
    menu.style.top = upward ? 'auto' : `${rect.bottom + 6}px`
    menu.style.bottom = upward ? `${innerHeight - rect.top + 6}px` : 'auto'
    // The top layer escapes both scrolling containers and native modal dialogs.
    menu.setAttribute('popover', 'manual')
    menu.showPopover?.()
    return () => { if (menu.hidePopover && menu.matches(':popover-open')) menu.hidePopover() }
  }, [open])
  useEffect(() => {
    if (!open) return
    if (disabled) { close(); return }
    const outside = (event: PointerEvent) => {
      if (!list.current?.contains(event.target as Node) && !trigger.current?.contains(event.target as Node)) close()
    }
    const scroll = (event: Event) => {
      if (!list.current?.contains(event.target as Node)) close()
    }
    document.addEventListener('pointerdown', outside)
    document.addEventListener('scroll', scroll, true)
    window.addEventListener('resize', close)
    window.visualViewport?.addEventListener('resize', close)
    return () => {
      document.removeEventListener('pointerdown', outside)
      document.removeEventListener('scroll', scroll, true)
      window.removeEventListener('resize', close)
      window.visualViewport?.removeEventListener('resize', close)
    }
  }, [open, disabled])
  useEffect(() => {
    if (open) list.current?.querySelector(`[data-index="${active}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [open, active])
  const keydown = (event: KeyboardEvent<HTMLButtonElement>) => {
    if (event.key === 'Tab') { close(); return }
    if (event.key === 'Escape' && open) { event.preventDefault(); event.stopPropagation(); close(); return }
    if (['Enter', ' '].includes(event.key)) {
      event.preventDefault()
      if (open) choose(active)
      else show()
      return
    }
    if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
      event.preventDefault()
      const position = enabled.indexOf(open ? active : selected)
      if (!enabled.length) return
      const index = event.key === 'Home' ? enabled[0] : event.key === 'End' ? enabled.at(-1)! : enabled[position < 0 ? (event.key === 'ArrowDown' ? 0 : enabled.length - 1) : (position + (event.key === 'ArrowDown' ? 1 : -1) + enabled.length) % enabled.length]
      show(index)
      return
    }
    if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
      event.preventDefault()
      const now = Date.now()
      search.current.text = now - search.current.time < 700 ? search.current.text + event.key : event.key
      search.current.time = now
      const text = search.current.text.toLocaleLowerCase()
      const prefix = [...text].every((c) => c === text[0]) ? text[0] : text
      const start = open ? active : selected
      const match = options.map((_, i) => (start + i + 1) % options.length).find((i) => !options[i].disabled && Children.toArray(options[i].children).join('').toLocaleLowerCase().startsWith(prefix))
      if (match !== undefined) show(match)
    }
  }
  return (
    <div className="custom-select">
      <button ref={trigger} id={id} type="button" className="select-trigger" role="combobox"
        aria-labelledby={`${id}-label`} aria-expanded={open} aria-haspopup="listbox" aria-controls={open ? listId : undefined}
        aria-activedescendant={open ? `${listId}-${active}` : undefined}
        disabled={disabled} onKeyDown={keydown} onClick={() => open ? close() : show()}>
        <span>{options[selected]?.children ?? value}</span><span aria-hidden="true">⌄</span>
      </button>
      {open && <div ref={list} id={listId} role="listbox" aria-labelledby={`${id}-label`} className="select-menu">
        {options.map((option, index) => <div key={option.value} id={`${listId}-${index}`} role="option"
          aria-selected={option.value === value} aria-disabled={option.disabled || undefined}
          data-index={index} className={`select-option${index === active ? ' active' : ''}`}
          onPointerMove={() => !option.disabled && setActive(index)}
          onPointerDown={(event) => event.preventDefault()} onClick={() => choose(index)}>
          <span>{option.children}</span><span aria-hidden="true">{option.value === value ? '✓' : ''}</span>
        </div>)}
      </div>}
    </div>
  )
}
