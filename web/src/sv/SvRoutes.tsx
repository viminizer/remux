import { useCallback, useEffect, useState, type ReactNode } from 'react'

import { api } from '../api'
import { HoldButton } from '../components/HoldButton'
import { Select } from '../components/Select'
import { toast } from '../components/Toast'
import type { Route } from '../router'
import type { HarnessRepo } from '../loops/types'
import type { SvDetail, SvList, SvTask } from './types'
import { PHASE, ago, needsYou, phaseDot } from './types'

const say = (e: unknown) => toast(e instanceof Error ? e.message : String(e))

/**
 * The supervisor list, polled only while something shows it: the screens,
 * or the drawer row.
 */
export function useSV(active: boolean) {
  const [data, setData] = useState<SvList | null>(null)
  const refresh = useCallback(() => api.svList().then(setData).catch(() => {}), [])
  useEffect(() => {
    if (!active) return
    void refresh()
    const t = setInterval(refresh, 5000)
    return () => clearInterval(t)
  }, [active, refresh])
  return { data, refresh }
}

/** Every supervisor screen, picked by route. */
export function SvRoutes({
  route,
  go,
  data,
  refresh,
  onClose,
}: {
  route: Route
  go: (r: Route) => void
  data: SvList | null
  refresh: () => Promise<void>
  onClose: () => void
}) {
  if (route.name === 'svNew') {
    return (
      <NewTask
        repos={data?.repos ?? []}
        onBack={() => go({ name: 'sv' })}
        onStart={async (v) => {
          const t = await api.svCreate(v)
          await refresh()
          go({ name: 'svTask', id: t.id })
        }}
      />
    )
  }
  if (route.name === 'svTask') {
    return <TaskScreen id={route.id} onBack={() => go({ name: 'sv' })} onWatch={(pane) => go({ name: 'pane', pane })} />
  }
  return <TaskList data={data} onBack={onClose} onRefresh={refresh} onOpen={(id) => go({ name: 'svTask', id })} onNew={() => go({ name: 'svNew' })} />
}

function Head({ title, sub, onBack, right }: { title: string; sub?: string; onBack: () => void; right?: ReactNode }) {
  return (
    <div className="screen-head">
      <button className="iconbtn" onClick={onBack} aria-label="Back">
        ←
      </button>
      <h2>
        {title}
        {sub && <small>{sub}</small>}
      </h2>
      {right}
    </div>
  )
}

// ── list ──────────────────────────────────────────────────────────────────

function TaskList({
  data,
  onBack,
  onRefresh,
  onOpen,
  onNew,
}: {
  data: SvList | null
  onBack: () => void
  onRefresh: () => Promise<void>
  onOpen: (id: string) => void
  onNew: () => void
}) {
  const tasks = data?.tasks ?? null
  // Waiting on Kevin first, then working, then the rest, newest first.
  const order = (t: SvTask) => (needsYou(t) ? 0 : t.phase === 'done' || t.phase === 'stopped' ? 2 : 1)
  const sorted = tasks ? [...tasks].sort((a, b) => order(a) - order(b)) : null
  const active = tasks?.filter((t) => t.phase === 'running' || t.phase === 'blocked').length ?? 0
  return (
    <div className="screen on gh-screen">
      <Head
        title="Supervisor"
        sub={tasks ? `${active} running` : 'reading…'}
        onBack={onBack}
        right={
          <button className="iconbtn" onClick={() => void onRefresh()} aria-label="Refresh">
            ⟳
          </button>
        }
      />
      <div className="screen-body loops-body">
        {sorted?.map((t) => (
          <button key={t.id} className="repo-card" onClick={() => onOpen(t.id)}>
            <span className="av">{t.agent === 'codex' ? 'cx' : 'cl'}</span>
            <span className="mid">
              <span className="nm">{t.goal}</span>
              <span className="st">
                <span className={`dot ${phaseDot(t.phase)}`} />
                <span className="loop-what">{t.phase === 'blocked' && t.question ? t.question : t.summary || PHASE[t.phase]}</span>
              </span>
              <span className="st loop-meta">
                {PHASE[t.phase]} · {t.id} · {ago(t.updated)}
              </span>
            </span>
            <span className="chev">›</span>
          </button>
        ))}
        {sorted && !sorted.length && (
          <div className="msg">
            <div className="glyph">◎</div>
            <h2>No tasks yet</h2>
            <p>Give the supervisor a task. You discuss it, approve its plan, and it does the rest with its workers.</p>
          </div>
        )}
      </div>
      <div className="foot-bar">
        <button className="go" onClick={onNew}>
          ✚ New task
        </button>
      </div>
    </div>
  )
}

// ── new ───────────────────────────────────────────────────────────────────

function NewTask({
  repos,
  onBack,
  onStart,
}: {
  repos: HarnessRepo[]
  onBack: () => void
  onStart: (v: { repo: string; brief: string; agent: 'claude' | 'codex' }) => Promise<void>
}) {
  const [repo, setRepo] = useState(repos[0]?.path ?? '')
  const [agent, setAgent] = useState<'claude' | 'codex'>('claude')
  const [brief, setBrief] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (!repo && repos[0]) setRepo(repos[0].path)
  }, [repo, repos])
  return (
    <div className="screen on gh-screen">
      <Head title="New task" onBack={onBack} />
      <div className="screen-body loop-form">
        <div className="field">
          <label htmlFor="sv-repo">Project</label>
          <Select id="sv-repo" value={repo} onChange={setRepo} disabled={busy}>
            {repos.map((r) => (
              <option key={r.path} value={r.path}>
                {r.slug} · {r.path}
              </option>
            ))}
          </Select>
        </div>
        <div className="field">
          <label htmlFor="sv-agent">Supervisor</label>
          <Select id="sv-agent" value={agent} onChange={(v) => setAgent(v as 'claude' | 'codex')} disabled={busy}>
            <option value="claude">Claude · default model, high effort</option>
            <option value="codex">Codex · default model, high effort</option>
          </Select>
        </div>
        <div className="field">
          <label htmlFor="sv-brief">The task</label>
          <textarea
            id="sv-brief"
            rows={8}
            value={brief}
            onChange={(e) => setBrief(e.target.value)}
            placeholder="What should be built or changed? The first line is its name."
          />
        </div>
        <p className="sheet-note">The supervisor studies the code first, then answers you in the chat.</p>
      </div>
      <div className="foot-bar">
        <button
          className="go"
          disabled={busy || !repo || !brief.trim()}
          onClick={() => {
            setBusy(true)
            onStart({ repo, brief, agent })
              .catch(say)
              .finally(() => setBusy(false))
          }}
        >
          {busy ? 'Starting…' : 'Start'}
        </button>
      </div>
    </div>
  )
}

// ── one task ──────────────────────────────────────────────────────────────

type Tab = 'status' | 'chat' | 'plan' | 'workers'

function TaskScreen({ id, onBack, onWatch }: { id: string; onBack: () => void; onWatch: (pane: string) => void }) {
  const [d, setD] = useState<SvDetail | null>(null)
  const [missing, setMissing] = useState(false)
  const [tab, setTab] = useState<Tab | null>(null)
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const load = useCallback(
    () =>
      api
        .svTask(id)
        .then((x) => {
          setD(x)
          setMissing(false)
        })
        .catch(() => setMissing(true)),
    [id],
  )
  useEffect(() => {
    void load()
    const t = setInterval(load, 3000)
    return () => clearInterval(t)
  }, [load])

  const t = d?.task
  // The tab that matters for the phase, until Kevin picks one.
  const shown: Tab =
    tab ?? (!t ? 'status' : t.phase === 'discussing' || t.phase === 'planning' ? 'chat' : t.phase === 'review' ? 'plan' : 'status')
  const act = async (fn: () => Promise<unknown>, done: string) => {
    setBusy(true)
    try {
      await fn()
      toast(done)
      setText('')
      await load()
    } catch (e) {
      say(e)
    } finally {
      setBusy(false)
    }
  }

  if (missing && !d) {
    return (
      <div className="screen on gh-screen">
        <Head title="Task" onBack={onBack} />
        <div className="msg">
          <h2>Task not found</h2>
        </div>
      </div>
    )
  }
  const finished = t?.phase === 'done' || t?.phase === 'stopped'
  const driverPane = d?.panes[`loop-${id}`]
  return (
    <div className="screen on gh-screen">
      <Head title={t?.goal ?? id} sub={t ? `${PHASE[t.phase]} · ${id}` : 'reading…'} onBack={onBack} />
      <div className="gh-seg">
        {(['status', 'chat', 'plan', 'workers'] as const).map((x) => (
          <button key={x} className={shown === x ? 'on' : ''} onClick={() => setTab(x)}>
            {x[0].toUpperCase() + x.slice(1)}
            {x === 'workers' && d && <span className="n">{d.workers.filter((w) => w.state === 'running').length}</span>}
          </button>
        ))}
      </div>

      <div className="screen-body loops-body">
        {!d || !t ? (
          <p className="loading">Reading…</p>
        ) : shown === 'status' ? (
          <StatusTab d={d} />
        ) : shown === 'chat' ? (
          <div className="sv-chat">
            {d.chat.map((m, i) => (
              <div key={i} className={`sv-msg ${m.from}`}>
                {m.text}
                <span className="sv-at">{ago(m.at)}</span>
              </div>
            ))}
            {(t.phase === 'discussing' || t.phase === 'planning') && d.chat.at(-1)?.from === 'kevin' && (
              <div className="sv-msg supervisor dim">{t.phase === 'planning' ? 'Writing the plan…' : 'Thinking…'}</div>
            )}
          </div>
        ) : shown === 'plan' ? (
          d.plan ? <pre className="sv-doc">{d.plan}</pre> : <p className="sheet-note">No plan yet. Discuss the task, then ask for the plan.</p>
        ) : (
          <>
            {driverPane && (
              <button className="repo-card" onClick={() => onWatch(driverPane)}>
                <span className="av">sv</span>
                <span className="mid">
                  <span className="nm">The supervisor</span>
                  <span className="st loop-meta">Watch its session live</span>
                </span>
                <span className="chev">›</span>
              </button>
            )}
            {d.workers.map((w) => {
              const pane = d.panes[`loop-${id}-w${String(w.n).padStart(2, '0')}`]
              return (
                <button key={w.id} className="repo-card" disabled={!pane} onClick={() => pane && onWatch(pane)}>
                  <span className="av">w{w.n}</span>
                  <span className="mid">
                    <span className="nm">
                      <span className="owner">{w.role} · </span>
                      {w.agent} {w.model || 'default'}
                      {w.readOnly ? ' · read-only' : ''}
                    </span>
                    <span className="st">
                      <span className={`dot ${w.state === 'running' ? 'working' : w.state === 'done' ? 'idle' : 'waiting'}`} />
                      <span className="loop-what">{w.why || w.state}</span>
                    </span>
                    <span className="st loop-meta">
                      {w.state} · {ago(w.started)}
                      {pane ? ' · tap to watch' : ''}
                    </span>
                  </span>
                </button>
              )
            })}
            {!d.workers.length && <p className="sheet-note">No workers yet. They start after you approve the plan.</p>}
          </>
        )}
      </div>

      {t && !finished && (
        <div className="foot-bar">
          {t.phase === 'review' ? (
            <>
              <div className="field">
                <textarea rows={2} value={text} onChange={(e) => setText(e.target.value)} placeholder="To send it back, say what to change" />
              </div>
              <div className="foot-row">
                <button className="go" disabled={busy || !text.trim()} onClick={() => void act(() => api.svBack(id, text), 'sent back')}>
                  Send back
                </button>
                <button className="go" disabled={busy} onClick={() => void act(() => api.svAct(id, 'approve'), 'approved - it starts now')}>
                  Approve
                </button>
              </div>
            </>
          ) : (
            <>
              {t.phase === 'blocked' && t.question && <p className="sheet-note">{t.question}</p>}
              <div className="field">
                <textarea
                  rows={2}
                  value={text}
                  onChange={(e) => setText(e.target.value)}
                  placeholder={t.phase === 'blocked' ? 'Your answer' : t.phase === 'discussing' ? 'Reply' : 'A note for the supervisor'}
                />
              </div>
              <div className="foot-row">
                <button className="go" disabled={busy || !text.trim()} onClick={() => void act(() => api.svChat(id, text), 'sent')}>
                  Send
                </button>
                {t.phase === 'discussing' && (
                  <button className="go" disabled={busy} onClick={() => void act(() => api.svAct(id, 'plan'), 'writing the plan')}>
                    Make the plan
                  </button>
                )}
              </div>
              {(t.phase === 'running' || t.phase === 'blocked' || t.phase === 'queued') && (
                <HoldButton label="Hold to stop the task" onConfirm={() => void act(() => api.svAct(id, 'stop'), 'stopped')} />
              )}
            </>
          )}
        </div>
      )}
    </div>
  )
}

const STEP: Record<string, string> = { done: '✓', running: '…', blocked: '!', next: '·' }

function StatusTab({ d }: { d: SvDetail }) {
  const t = d.task
  const msgs = d.events.filter((e) => e.kind === 'msg')
  return (
    <>
      <div className="question">{t.phase === 'blocked' && t.question ? t.question : t.summary || PHASE[t.phase]}</div>
      {!!t.steps?.length && (
        <Block title="Plan steps">
          {t.steps.map((s, i) => (
            <div key={i} className={`sv-step ${s.state}`}>
              <span className="n">{STEP[s.state] ?? '·'}</span>
              <span>
                {s.title}
                {s.note && <small>{s.note}</small>}
              </span>
            </div>
          ))}
        </Block>
      )}
      {!!t.changes?.length && <List title="Changed from the plan" items={t.changes} />}
      {!!t.evidence?.length && <List title="Evidence" items={t.evidence} />}
      {!!msgs.length && <List title="Messages with other supervisors" items={msgs.map((e) => `${ago(e.at)} · ${e.text}`)} />}
      {d.retro && (
        <Block title="Retro">
          <pre className="sv-doc">{d.retro}</pre>
        </Block>
      )}
      <details className="settings-ignored">
        <summary>Activity · {d.events.length}</summary>
        <pre className="loop-log">
          {d.events
            .slice()
            .reverse()
            .map((e) => `${ago(e.at).padStart(3)} ${e.kind.padEnd(8)} ${e.worker ? e.worker.split('/').pop() + ' ' : ''}${e.text}`)
            .join('\n')}
        </pre>
      </details>
    </>
  )
}

function Block({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="sv-block">
      <h3>{title}</h3>
      {children}
    </section>
  )
}

function List({ title, items }: { title: string; items: string[] }) {
  return (
    <Block title={title}>
      <ul className="sv-list">
        {items.map((x, i) => (
          <li key={i}>{x}</li>
        ))}
      </ul>
    </Block>
  )
}
