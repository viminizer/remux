import { useRef, useState } from 'react'

/**
 * Press and hold to confirm, with a filling bar.
 *
 * Kill pane / window / session use this instead of a modal: there is no dialog
 * to mis-tap, and it cannot fire by accident while scrolling a list on a phone.
 */
export function HoldButton({
  label,
  onConfirm,
  ms = 800,
  className = '',
}: {
  label: string
  onConfirm: () => void
  ms?: number
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
      className={`mi hold danger ${holding ? 'holding' : ''} ${className}`}
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
