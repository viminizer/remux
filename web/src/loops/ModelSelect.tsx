import { useId } from 'react'
import { Select } from '../components/Select'

export function ModelSelect({ label, value, onChange }: {
  label: string
  value: string
  onChange: (value: string) => void
}) {
  const id = useId()
  return (
    <div className="field">
      <label id={`${id}-label`} htmlFor={id}>{label}</label>
      <Select id={id} value={value} onChange={onChange}>
        <option value="">CLI default</option>
        <option value="sonnet">Sonnet</option>
        <option value="opus">Opus</option>
      </Select>
    </div>
  )
}
