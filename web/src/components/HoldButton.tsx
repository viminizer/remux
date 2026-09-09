import { useRef, useState } from 'react'

/**
 * Press and hold to confirm, with a filling bar.
 *
 * Kill pane / window / session use this instead of a modal: there is no dialog
 * to mis-tap, and it cannot fire by accident while scrolling a list on a phone.
 *
 * `danger` is what the hold is saying, not whether there is one. Killing a pane
 * is destructive and reads red; splitting an agent's pane only reflows its TUI
 * and is undone by closing the new pane, so it holds in the ordinary colour. A
 * red bar on a reversible action spends the red on the wrong thing, and the
 * next real warning is worth less for it.
 */
export function HoldButton({
  label,
  onConfirm,
  ms = 800,
  danger = true,
  className = '',
}: {
  label: string
  onConfirm: () => void
  ms?: number
  danger?: boolean
  className?: string
}) {
  const [holding, setHolding] = useState(false)
  const timer = useRef<number | null>(null)

  const start = (e: React.PointerEvent) => {
    e.preventDefault()
    setHolding(true)
    timer.current = window.setTimeout(() => {
      setHolding(false)
      onConfirm()
    }, ms)
  }

  const cancel = () => {
    if (timer.current !== null) {
      clearTimeout(timer.current)
      timer.current = null
    }
    setHolding(false)
  }

  return (
    <button
      className={`mi hold ${danger ? 'danger' : 'calm'} ${holding ? 'holding' : ''} ${className}`}
      onPointerDown={start}
      onPointerUp={cancel}
      onPointerLeave={cancel}
      onPointerCancel={cancel}
    >
      <span className="fill" />
      {label} <small>hold</small>
    </button>
  )
}
