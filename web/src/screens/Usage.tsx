import { useEffect, useRef, useState } from 'react'

import { api } from '../api'
import type { ModelUsage } from '../types'

function localTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime())
    ? 'Reset time unavailable'
    : date.toLocaleString(undefined, {
        month: 'short',
        day: 'numeric',
        hour: 'numeric',
        minute: '2-digit',
        timeZoneName: 'short',
      })
}

export function UsageScreen({ onBack }: { onBack: () => void }) {
  const [data, setData] = useState<ModelUsage | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const controller = useRef<AbortController | null>(null)
  async function load(refresh = false) {
    controller.current?.abort()
    const request = new AbortController()
    controller.current = request
    setLoading(true)
    setError('')
    try {
      const next = await api.usage(refresh, request.signal)
      if (!request.signal.aborted) setData(next)
    } catch (e) {
      if (!request.signal.aborted)
        setError(
          e instanceof Error ? e.message : 'Usage unavailable. Try refreshing.',
        )
    } finally {
      if (!request.signal.aborted) setLoading(false)
    }
  }
  useEffect(() => {
    void load()
    return () => controller.current?.abort()
  }, [])
  return (
    <div className="screen on">
      <div className="screen-head">
        <button
          className="iconbtn"
          aria-label="Back to Settings"
          onClick={onBack}
        >
          ←
        </button>
        <h2>Model usage</h2>
        <button
          className="usage-refresh"
          disabled={loading}
          onClick={() => void load(true)}
        >
          {loading ? 'Checking…' : 'Refresh'}
        </button>
      </div>
      <div className="screen-body usage-body" aria-busy={loading}>
        <p className="usage-intro">
          Your account limits, checked on the Mac. Refresh whenever you need an
          update.
        </p>
        <div role="status" aria-live="polite">
          {loading && (
            <p className="usage-note">Reading native account usage…</p>
          )}
          {(error || data?.error) && (
            <p className="usage-warning">{error || data?.error}</p>
          )}
        </div>
        <div className="usage-accounts">
          {data?.accounts.map((account) => (
            <section
              className="sec"
              key={account.provider}
              aria-label={`${account.provider} usage`}
            >
              <div className="card usage-card">
                <div className="usage-account">
                  <div className="usage-provider">
                    {account.provider}
                    <span>{account.plan}</span>
                  </div>
                  <div className="usage-email">{account.email}</div>
                </div>
                {(account.error || error) && (
                  <p className="usage-warning" role="status">
                    {account.stale || error ? 'Previous snapshot · ' : ''}
                    {account.error ||
                      'Refresh failed. Showing the last successful reading.'}
                  </p>
                )}
                {account.metrics?.map((metric) => (
                  <div className="usage-metric" key={metric.label}>
                    <div className="usage-metric-head">
                      <span>{metric.label}</span>
                      <strong
                        className={
                          metric.remaining_percent <= 10 ? 'usage-low' : ''
                        }
                      >
                        {Math.round(metric.remaining_percent)}%{' '}
                        <small>LEFT</small>
                      </strong>
                    </div>
                    <div
                      className="usage-meter"
                      role="meter"
                      aria-label={`${metric.label} remaining`}
                      aria-valuemin={0}
                      aria-valuemax={100}
                      aria-valuenow={metric.remaining_percent}
                    >
                      <span
                        style={{
                          width: `${metric.remaining_percent}%`,
                          background:
                            metric.remaining_percent <= 10
                              ? 'var(--red)'
                              : metric.remaining_percent <= 25
                                ? 'var(--yellow)'
                                : 'var(--green)',
                        }}
                      />
                    </div>
                    <div className="usage-reset">
                      {metric.resets_at ? (
                        <>
                          Resets{' '}
                          <time dateTime={metric.resets_at}>
                            {localTime(metric.resets_at)}
                          </time>
                        </>
                      ) : (
                        'Reset time unavailable'
                      )}
                    </div>
                  </div>
                ))}
              <div className="usage-checked">
                {account.checked_at ? (
                  <>
                    Last checked{' '}
                    <time dateTime={account.checked_at}>
                      {localTime(account.checked_at)}
                    </time>
                    {(account.stale || error) && ' · stale'}
                  </>
                ) : (
                  'No verified reading yet'
                )}
              </div>
            </div>
          </section>
        ))}
        </div>
        <p className="usage-note">
          Reset times use this device’s timezone. Only limits reported by the
          provider are shown.
        </p>
      </div>
    </div>
  )
}
