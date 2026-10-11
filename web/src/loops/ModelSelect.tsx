export function ModelSelect({ label, value, onChange }: {
  label: string
  value: string
  onChange: (value: string) => void
}) {
  return (
    <div className="field">
      <label>
        {label}
        <select value={value} onChange={(e) => onChange(e.target.value)}>
          <option value="">CLI default</option>
          <option value="sonnet">Sonnet</option>
          <option value="opus">Opus</option>
        </select>
      </label>
    </div>
  )
}
