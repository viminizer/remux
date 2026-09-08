import { useEffect, useState } from 'react'

let show: (msg: string) => void = () => {}

/** Fire a toast from anywhere, including non-component code. */
export function toast(msg: string) {
  show(msg)
}

export function Toaster() {
  const [msg, setMsg] = useState('')
  const [on, setOn] = useState(false)

  useEffect(() => {
    let t: number | undefined
    show = (m: string) => {
      setMsg(m)
      setOn(true)
      clearTimeout(t)
      t = window.setTimeout(() => setOn(false), 1600)
    }
    return () => clearTimeout(t)
  }, [])

  return <div className={`toast ${on ? 'on' : ''}`}>{msg}</div>
}
