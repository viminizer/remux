import { useEffect, useId, useRef, useState } from 'react'
import { api } from '../api'
import { Select } from '../components/Select'
import type { HarnessRepo, Loop, LoopProject, LoopStart, Preset } from './types'
import { ModelSelect } from './ModelSelect'

const OTHER = '__other__'
type Form = ReturnType<typeof formFor> & { presetName?: string }
const message = (e: unknown) => (e instanceof Error ? e.message : String(e))
const builtin = ['code-quality', 'frontend', 'backend']
const descriptions = [
  [
    'code-quality',
    'Code quality',
    'Audit, fix and review the codebase, one task at a time.',
  ],
  ['frontend', 'Frontend', 'Build and improve the interface and user flows.'],
  ['backend', 'Backend', 'Work on APIs, services and data behavior.'],
  ['custom', 'Custom', 'Follow your own scope and session instructions.'],
]

export function StartLoop({
  repos: offeredRepos,
  running,
  last,
  presets,
  onBack,
  onStart,
  onSavePresets,
}: {
  repos: HarnessRepo[]
  running: Loop[]
  last: LoopStart | null
  presets: Preset[]
  onBack: () => void
  onStart: (v: LoopStart) => Promise<void>
  onSavePresets: (p: Preset[]) => Promise<void>
}) {
  const [repos, setRepos] = useState(offeredRepos)
  const [choiceError, setChoiceError] = useState('')
  useEffect(() => {
    const controller = new AbortController()
    api
      .loopProjects(controller.signal)
      .then((value) => {
        if (!controller.signal.aborted) setRepos(value)
      })
      .catch((e) => {
        if (!controller.signal.aborted) setChoiceError(message(e))
      })
    return () => controller.abort()
  }, [])
  const first = last?.repo ?? repos[0]?.path ?? ''
  const [repo, setRepo] = useState(
    repos.some((r) => r.path === first) ? first : OTHER,
  )
  const [path, setPath] = useState(first)
  const target = repo === OTHER ? path.trim() : repo
  const [form, setForm] = useState<Form>(
    () => formFor(first, running, last),
  )
  const {
    claude,
    codex,
    models,
    reviews,
    supervise,
    supAgent,
    supModel,
    cleanup,
    cleanAgent,
    cleanDry,
    scope,
    instr,
    mode,
  } = form
  const change = (v: Partial<Form>) => {
    setForm((f) => ({ ...f, ...v }))
    setError('')
  }
  const drafts = useRef(new Map<string, Form>())
  const ordinary = useRef<Form | null>(null)
  const scopeDrafts = useRef(new Map<string, Form>())
  const [customEditing, setCustomEditing] = useState(
    !builtin.includes(formFor(first, running, last).scope),
  )
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [metadata, setMetadata] = useState<{
    key: string
    value: LoopProject
  } | null>(null)
  const [metaError, setMetaError] = useState('')
  const [retry, setRetry] = useState(0)
  const [manage, setManage] = useState(false)
  const [editing, setEditing] = useState<Preset | null>(null)
  const [originalName, setOriginalName] = useState('')
  const [deleting, setDeleting] = useState<Preset | null>(null)
  const [saveBusy, setSaveBusy] = useState(false)
  const [saveError, setSaveError] = useState('')
  const [previousTeam, setPreviousTeam] = useState<{
    key: string
    form: Form
  } | null>(null)
  const metaKey = `${target}\n${scope || 'full'}`
  const info = metadata?.key === metaKey ? metadata.value : null
  const quality = scope === 'code-quality'
  useEffect(() => {
    const controller = new AbortController()
    setMetaError('')
    if (!target) return () => controller.abort()
    const timer = setTimeout(
      () => {
        api
          .loopProject(
            target,
            /^[a-z0-9][a-z0-9-]{0,19}$/.test(scope) ? scope : 'full',
            controller.signal,
          )
          .then((value) => {
            if (controller.signal.aborted) return
            setMetadata({ key: metaKey, value })
            if (scope === 'code-quality' && !value.quality)
              setForm((f) => ({ ...(ordinary.current ?? f), scope: '' }))
          })
          .catch((e) => {
            if (!controller.signal.aborted) setMetaError(message(e))
          })
      },
      repo === OTHER ? 350 : 0,
    )
    return () => {
      controller.abort()
      clearTimeout(timer)
    }
  }, [target, scope, metaKey, retry, repo])

  const selectProject = (next: string) => {
    drafts.current.set(target, form)
    setRepo(next)
    ordinary.current = null
    const nextTarget = next === OTHER ? path.trim() : next
    const nextForm: Form =
      drafts.current.get(nextTarget) ?? formFor(nextTarget, running, last)
    const prior =
      drafts.current.has(nextTarget) ||
      runningIn(nextTarget, running, false).length > 0 ||
      runningIn(nextTarget, running, true).length > 0 ||
      last?.repo === nextTarget
    const keepQuality = quality && !prior
    setForm(keepQuality ? { ...nextForm, scope: 'code-quality' } : nextForm)
    setCustomEditing(
      !keepQuality && (!!nextForm.presetName || !builtin.includes(nextForm.scope)),
    )
    setError('')
  }
  const selectScope = (next: string, preset?: Preset) => {
    scopeDrafts.current.set(scopeDraftKey(target, form.scope, form.presetName), form)
    setCustomEditing(!!preset || !builtin.includes(next))
    let nextForm = form
    if (next === 'code-quality' && !quality) {
      ordinary.current = form
      nextForm = {
        ...form,
        claude: 0,
        codex: 1,
        reviews: 0,
        supervise: false,
        cleanup: false,
      }
    }
    if (next !== 'code-quality' && quality)
      nextForm =
        ordinary.current ??
        formFor(
          target,
          running.filter((l) => l.scope !== 'code-quality'),
          last?.scope === 'code-quality' ? null : last,
        )
    change(
      scopeSelection(
        target, next, nextForm, scopeDrafts.current, preset,
        presets.find((p) => p.scope === next),
      ),
    )
  }
  const provider = (agent: 'codex' | 'claude') => {
    setPreviousTeam({ key: scopeDraftKey(target, scope, form.presetName), form })
    change({
      claude: agent === 'claude' ? 1 : 0,
      codex: agent === 'codex' ? 1 : 0,
    })
  }
  const effectiveTarget = info?.repo ?? target
  const live = runningIn(effectiveTarget, running, quality)
  const request: LoopStart = {
    repo: effectiveTarget,
    agents: quality
      ? [claude === 1 && codex === 0 ? 'claude' : 'codex']
      : [
          ...Array<'claude'>(claude).fill('claude'),
          ...Array<'codex'>(codex).fill('codex'),
        ],
    models: quality
      ? [claude === 1 && codex === 0 ? (models[0] ?? 'opus') : '']
      : [
          ...Array.from({ length: claude }, (_, i) => models[i] ?? 'opus'),
          ...Array<string>(codex).fill(''),
        ],
    review: !quality && reviews > 0,
    reviews: quality ? 0 : reviews,
    supervise: !quality && supervise,
    superviseAgent: supAgent,
    superviseModel: supAgent === 'claude' ? supModel : '',
    cleanup: !quality && cleanup,
    cleanupAgent: cleanAgent,
    cleanupDry: cleanDry,
    scope,
    instructions: instr,
    instrMode: mode,
  }
  const activeRoles: ('build' | 'review' | 'supervise')[] = quality
    ? ['build']
    : [
        ...(claude + codex > 0 ? (['build'] as const) : []),
        ...(reviews ? (['review'] as const) : []),
        ...(supervise ? (['supervise'] as const) : []),
      ]
  const impact = previewImpact(request, running)
  const valid =
    !!target &&
    /^[a-z0-9][a-z0-9-]{0,19}$/.test(scope) &&
    (quality
      ? !!info?.quality
      : request.agents.length + reviews + Number(supervise) + Number(cleanup) >
        0)
  const editPreset = (p: Preset, original = p.name) => {
    setOriginalName(original)
    setEditing({ ...p })
    setSaveError('')
  }
  const savePresets = async (next: Preset[], done: () => void) => {
    setSaveBusy(true)
    setSaveError('')
    try {
      await onSavePresets(next)
      done()
    } catch (e) {
      setSaveError(message(e))
    } finally {
      setSaveBusy(false)
    }
  }
  const sameSlug =
    repos.filter((r) => r.slug === repos.find((x) => x.path === target)?.slug)
      .length > 1

  return (
    <div className="screen on gh-screen start-loop">
      <div className="screen-head">
        <button className="iconbtn" onClick={onBack} aria-label="Back">
          ←
        </button>
        <h2>Start loop</h2>

      </div>
      <div className="screen-body loops-body">
        <section className="start-section">
          <h3>
            <span>01</span> Project
          </h3>
          <div className="field">
            <label className="sr-only" id="start-project-label" htmlFor="start-project">
              Project
            </label>
            <Select
              id="start-project"
              value={repo}
              onChange={(value) => selectProject(value)}
              disabled={busy}
            >
              {repo !== OTHER && !repos.some((r) => r.path === repo) && (
                <option value={repo}>Unavailable project · {repo}</option>
              )}
              {repos.map((r) => (
                <option key={r.path} value={r.path}>
                  {r.slug}
                  {repos.filter((x) => x.slug === r.slug).length > 1
                    ? ` · ${r.path}`
                    : ''}
                </option>
              ))}
              <option value={OTHER}>Other path…</option>
            </Select>
          </div>
          {choiceError && (
            <p className="muted">
              Project choices could not be verified; showing known paths.{' '}
              {choiceError}
            </p>
          )}
          {repo === OTHER && (
            <div className="field">
              <label htmlFor="start-path">Repo path on the Mac</label>
              <input
                id="start-path"
                value={path}
                onChange={(e) => {
                  drafts.current.set(target, form)
                  setPath(e.target.value)
                }}
                spellCheck={false}
                autoCapitalize="off"
                placeholder="/Users/you/projects/remux"
                disabled={busy}
              />
            </div>
          )}
          {sameSlug && <p className="start-path">{target}</p>}
        </section>
        <section className="start-section">
          <div className="start-section-head">
            <h3>
              <span>02</span> Scope
            </h3>
            <button
              className="linkbtn"
              onClick={() => {
                setManage(true)
                setSaveError('')
              }}
            >
              Manage scopes
            </button>
          </div>
          <fieldset className="scope-choices" disabled={busy}>
            <legend className="sr-only">Scope</legend>
            {descriptions.map(([id, name, description]) => (
              <label className="scope-choice" key={id}>
                <input
                  type="radio"
                  name="start-scope"
                  value={id}
                  checked={
                    id === 'custom'
                      ? customEditing
                      : !customEditing && scope === id
                  }
                  disabled={id === 'code-quality' && !info?.quality}
                  onChange={() => selectScope(id === 'custom' ? 'full' : id)}
                />
                <span>
                  <strong>{name}</strong>
                  <small>
                    {description}
                    {id === 'code-quality' && !info?.quality
                      ? metaError
                        ? ' Availability unknown.'
                        : info
                          ? ' No workflow configured.'
                          : ' Checking workflow…'
                      : ''}
                  </small>
                </span>
                {id === 'code-quality' && (
                  <span className="scope-tag">1 AGENT</span>
                )}
              </label>
            ))}
          </fieldset>

          {metaError && (
            <p className="loaderr" role="alert">
              Could not read project workflow: {metaError}{' '}
              <button
                className="linkbtn"
                onClick={() => setRetry((n) => n + 1)}
              >
                Retry
              </button>
            </p>
          )}
          {customEditing && presets.length > 0 && (
            <div className="field">
              <label id="custom-saved-label" htmlFor="custom-saved">Saved scope</label>
              <Select
                id="custom-saved"
                value={presets.some((p) => p.name === form.presetName)
                  ? `preset:${form.presetName}` : '__new__'}
                onChange={(value) => {
                  const preset = presets.find((p) => `preset:${p.name}` === value)
                  selectScope(preset?.scope ?? 'full', preset)
                }}
              >
                <option value="__new__">Your own scope…</option>
                {presets.map((p) => (
                  <option key={p.name} value={`preset:${p.name}`}>{p.name}</option>
                ))}
              </Select>
            </div>
          )}
          {quality ? (
            <div className="quality-team">
              <div className="field">
                <label htmlFor="quality-agent">Loop agent</label>
                <Select id="quality-agent" value={claude === 1 && codex === 0 ? 'claude' : 'codex'} onChange={(value) => provider(value as 'codex' | 'claude')} disabled={busy}>
                  <option value="codex">Codex</option>
                  <option value="claude">Claude</option>
                </Select>
              </div>
              {claude === 1 && codex === 0 && <ModelSelect label="Claude model" value={models[0] ?? 'opus'} onChange={(value) => change({ models: [value] })} />}
              <strong>1 coordinator · serial audits</strong>
              <p>Audit → Fix → Review gate 1 → Review gate 2</p>
              <small>
                Fresh internal workers and two reviews, including Codex review.
                No extra Remux reviewer loops.
              </small>
            </div>
          ) : (
            (scope || customEditing) && (
              <>
                <p className="muted">Agent for this work</p>
                <div className="seg start-providers">
                  <button
                    aria-pressed={codex === 1 && claude === 0}
                    className={codex === 1 && claude === 0 ? 'on' : ''}
                    onClick={() => provider('codex')}
                  >
                    Codex
                  </button>
                  <button
                    aria-pressed={claude === 1 && codex === 0}
                    className={claude === 1 && codex === 0 ? 'on' : ''}
                    onClick={() => provider('claude')}
                  >
                    Claude · {models[0] || 'CLI default'}
                  </button>
                </div>
                {previousTeam?.key === scopeDraftKey(target, scope, form.presetName) && (
                  <button
                    className="linkbtn"
                    onClick={() => {
                      const previous = previousTeam.form
                      setPreviousTeam({ key: scopeDraftKey(target, scope, form.presetName), form })
                      change({
                        claude: previous.claude,
                        codex: previous.codex,
                        models: previous.models,
                      })
                    }}
                  >
                    Restore previous builder team
                  </button>
                )}
                {customEditing && (
                  <div className="field">
                    <label htmlFor="custom-scope">Scope name</label>
                    <input
                      id="custom-scope"
                      value={scope}
                      onChange={(e) => change({ scope: e.target.value })}
                      spellCheck={false}
                      autoCapitalize="off"
                      aria-describedby="scope-help"
                    />
                    <small id="scope-help">
                      1–20 lowercase letters, digits or dashes; start with a
                      letter or digit.
                    </small>
                  </div>
                )}
                <details className="start-advanced">
                  <summary>Advanced · team & instructions</summary>
                  <p className="muted">
                    Target team for ordinary loops in this project.
                  </p>
                  <Stepper
                    name="Claude"
                    value={claude}
                    onChange={(claude) => change({ claude })}
                  />
                  <Stepper
                    name="Codex"
                    value={codex}
                    onChange={(codex) => change({ codex })}
                  />
                  {Array.from({ length: claude }, (_, i) => (
                    <ModelSelect
                      key={i}
                      label={`Claude ${i + 1} model`}
                      value={models[i] ?? 'opus'}
                      onChange={(v) =>
                        change({
                          models: Array.from({ length: claude }, (_, j) =>
                            j === i ? v : (models[j] ?? 'opus'),
                          ),
                        })
                      }
                    />
                  ))}
                  <Stepper
                    name="Review (Codex)"
                    value={reviews}
                    onChange={(reviews) => change({ reviews })}
                  />
                  <label className="start-check">
                    <input
                      type="checkbox"
                      checked={supervise}
                      onChange={(e) => change({ supervise: e.target.checked })}
                    />
                    Supervisor
                  </label>
                  {supervise && (
                    <>
                      <div className="field">
                        <label id="supervisor-agent-label" htmlFor="supervisor-agent">
                          Supervisor provider
                        </label>
                        <Select
                          id="supervisor-agent"
                          value={supAgent}
                          onChange={(value) =>
                            change({
                              supAgent: value as 'claude' | 'codex',
                            })
                          }
                        >
                          <option value="codex">Codex</option>
                          <option value="claude">Claude</option>
                        </Select>
                      </div>
                      {supAgent === 'claude' && (
                        <ModelSelect
                          label="Supervisor model"
                          value={supModel}
                          onChange={(supModel) => change({ supModel })}
                        />
                      )}
                    </>
                  )}
                  <label className="start-check">
                    <input
                      type="checkbox"
                      checked={cleanup}
                      onChange={(e) => change({ cleanup: e.target.checked })}
                    />
                    Cleanup
                  </label>
                  {cleanup && (
                    <>
                      <div className="field">
                        <label id="cleanup-agent-label" htmlFor="cleanup-agent">Cleanup provider</label>
                        <Select
                          id="cleanup-agent"
                          value={cleanAgent}
                          onChange={(value) =>
                            change({
                              cleanAgent: value as 'claude' | 'codex',
                            })
                          }
                        >
                          <option value="codex">Codex</option>
                          <option value="claude">Claude</option>
                        </Select>
                      </div>
                      <label className="start-check">
                        <input
                          type="checkbox"
                          checked={cleanDry}
                          onChange={(e) =>
                            change({ cleanDry: e.target.checked })
                          }
                        />
                        Dry run · report only
                      </label>
                    </>
                  )}
                  {info && !activeRoles.every((role) => !!info.roles[role]) ? (
                    <div className="field">
                      <label htmlFor="session-instructions">
                        Session instructions for roles without a project file
                      </label>
                      <textarea
                        id="session-instructions"
                        value={instr}
                        onChange={(e) => change({ instr: e.target.value })}
                        rows={4}
                      />
                    </div>
                  ) : (
                    <p className="muted">
                      {info
                        ? 'Project role files supply the session instructions. Your draft text is preserved.'
                        : 'Read project instructions before editing guidance.'}
                    </p>
                  )}
                  <div className="field">
                    <label id="instruction-mode-label" htmlFor="instruction-mode">Project defaults</label>
                    <Select
                      id="instruction-mode"
                      value={mode}
                      onChange={(value) =>
                        change({ mode: value as 'add' | 'replace' })
                      }
                    >
                      <option value="add">Add to project defaults</option>
                      <option value="replace">Replace project defaults</option>
                    </Select>
                  </div>
                  <button
                    className="linkbtn"
                    onClick={() => {
                      setManage(true)
                      editPreset(
                        {
                          name: scope,
                          scope,
                          instructions: instr,
                          instrMode: mode,
                        },
                        '',
                      )
                    }}
                  >
                    Save as a scope…
                  </button>
                </details>
              </>
            )
          )}
        </section>
        <section className="start-section">
          <h3>
            <span>03</span> Summary
          </h3>
          <div className="start-impact" aria-live="polite">
            <strong>
              {scope
                ? `${repos.find((r) => r.path === target)?.slug ?? target} · ${quality ? 'Code quality' : scope}`
                : 'Choose a scope'}
            </strong>
            {scope && !info && (
              <p className="muted" role="status">
                {metaError
                  ? 'Impact preview unavailable until project files can be read.'
                  : 'Reading project workflow before previewing changes…'}
              </p>
            )}
            {scope &&
              info &&
              impact.map((row, i) => (
                <div className={`impact-row ${row.kind}`} key={i}>
                  <em>{row.kind.toUpperCase()}</em>
                  <span>{row.text}</span>
                </div>
              ))}
            <p className="muted">
              Starts are immediate. Retained loops use new settings from the
              next task. Lowered counts stop working loops after their task and
              idle loops now. Other projects are untouched.
            </p>
          </div>
          <details className="start-advanced">
            <summary>Effective instructions</summary>
            {!info ? (
              <p className="muted">
                {metaError
                  ? 'Project instructions unavailable. Retry the lookup before starting.'
                  : 'Reading selected project…'}
              </p>
            ) : (
              <>
                <p className="muted">
                  {mode === 'add'
                    ? 'Project defaults are included before session guidance.'
                    : 'Project defaults are replaced. Core agent rules still apply.'}
                </p>
                {mode === 'add' && (
                  <Instruction text={info.defaults} label="Project defaults" />
                )}
                {activeRoles.map((role) => (
                  <Instruction
                    key={role}
                    label={`${role} · ${info.roles[role]?.source ?? 'the form'}`}
                    text={info.roles[role]?.text ?? instr}
                  />
                ))}
                {cleanup && !quality && (
                  <p className="muted">
                    Cleanup uses its own built-in cleanup rules, not form
                    guidance.
                  </p>
                )}
              </>
            )}
          </details>
        </section>
        {error && (
          <p className="loaderr" role="alert">
            {error}
          </p>
        )}
      </div>
      <div className="foot-bar">
        <button
          className="go"
          disabled={!valid || !info || busy}
          onClick={async () => {
            setBusy(true)
            setError('')
            try {
              await onStart(request)
            } catch (e) {
              setError(message(e))
            } finally {
              setBusy(false)
            }
          }}
        >
          {busy
            ? 'Applying…'
            : !scope
              ? 'Choose a scope'
              : live.length
                ? 'Apply loop changes'
                : quality
                  ? 'Start code quality'
                  : `Start ${scope === 'full' ? 'custom' : scope}`}
        </button>
      </div>
      {manage && (
        <Dialog
          title={
            editing
              ? 'Edit saved scope'
              : deleting
                ? `Delete “${deleting.name}”?`
                : 'Manage scopes'
          }
          onClose={() => {
            if (!saveBusy) {
              setManage(false)
              setEditing(null)
              setDeleting(null)
            }
          }}
        >
          {editing ? (
            <>
              <div className="field">
                <label htmlFor="preset-name">Saved name</label>
                <input
                  id="preset-name"
                  value={editing.name}
                  onChange={(e) =>
                    setEditing({ ...editing, name: e.target.value })
                  }
                />
              </div>
              <div className="field">
                <label htmlFor="preset-scope">Scope</label>
                <input
                  id="preset-scope"
                  value={editing.scope}
                  onChange={(e) =>
                    setEditing({ ...editing, scope: e.target.value })
                  }
                />
              </div>
              <div className="field">
                <label htmlFor="preset-instructions">
                  Session instructions
                </label>
                <textarea
                  id="preset-instructions"
                  rows={5}
                  value={editing.instructions}
                  onChange={(e) =>
                    setEditing({ ...editing, instructions: e.target.value })
                  }
                />
              </div>
              <div className="field">
                <label id="preset-mode-label" htmlFor="preset-mode">Instruction mode</label>
                <Select
                  id="preset-mode"
                  value={editing.instrMode}
                  onChange={(value) =>
                    setEditing({
                      ...editing,
                      instrMode: value as 'add' | 'replace',
                    })
                  }
                >
                  <option value="add">Add to project defaults</option>
                  <option value="replace">Replace project defaults</option>
                </Select>
              </div>
              <button
                className="go"
                disabled={
                  saveBusy ||
                  !editing.name.trim() ||
                  !/^[a-z0-9][a-z0-9-]{0,19}$/.test(editing.scope)
                }
                onClick={() => {
                  if (
                    presets.some(
                      (p) =>
                        p.name === editing.name.trim() &&
                        p.name !== originalName,
                    )
                  ) {
                    setSaveError('A saved scope already uses this name.')
                    return
                  }
                  void savePresets(
                    [
                      ...presets.filter((p) => p.name !== originalName),
                      { ...editing, name: editing.name.trim() },
                    ],
                    () => setEditing(null),
                  )
                }}
              >
                {saveBusy ? 'Saving…' : 'Save scope'}
              </button>
              <button
                className="cancel"
                disabled={saveBusy}
                onClick={() => setEditing(null)}
              >
                Cancel edit
              </button>
            </>
          ) : deleting ? (
            <>
              <p>
                Remove the saved scope “{deleting.name}”? Running loops and
                their assignments stay in place.
              </p>
              <button
                className="go danger"
                disabled={saveBusy}
                onClick={() =>
                  void savePresets(
                    presets.filter((p) => p.name !== deleting.name),
                    () => setDeleting(null),
                  )
                }
              >
                {saveBusy ? 'Deleting…' : `Delete ${deleting.name}`}
              </button>
              <button
                className="cancel"
                disabled={saveBusy}
                onClick={() => setDeleting(null)}
              >
                Keep scope
              </button>
            </>
          ) : (
            <>
              <p className="muted">
                Saved guidance. Editing or deleting never changes a running
                loop. Built-in scopes remain available.
              </p>
              {presets.map((p) => (
                <div className="manage-scope-row" key={p.name}>
                  <strong>{p.name}</strong>
                  <button className="linkbtn" onClick={() => editPreset(p)}>
                    Edit
                  </button>
                  <button
                    className="linkbtn danger"
                    onClick={() => {
                      setDeleting(p)
                      setSaveError('')
                    }}
                  >
                    Delete
                  </button>
                </div>
              ))}
              <button
                className="linkbtn"
                onClick={() =>
                  editPreset(
                    {
                      name: '',
                      scope: 'custom',
                      instructions: '',
                      instrMode: 'add',
                    },
                    '',
                  )
                }
              >
                New saved scope
              </button>
            </>
          )}
          {saveError && (
            <p className="loaderr" role="alert">
              {saveError}
            </p>
          )}
        </Dialog>
      )}

    </div>
  )
}

function Instruction({ label, text }: { label: string; text: string }) {
  return (
    <div className="instruction-preview">
      <strong>{label}</strong>
      <pre>{text || 'No additional instructions.'}</pre>
    </div>
  )
}

function Dialog({
  title,
  children,
  onClose,
}: {
  title: string
  children: React.ReactNode
  onClose: () => void
}) {
  const titleId = useId()
  const ref = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const dialog = ref.current
    dialog?.showModal()
    return () => dialog?.close()
  }, [])
  return (
    <dialog
      ref={ref}
      className="start-dialog"
      aria-labelledby={titleId}
      onCancel={(e) => {
        e.preventDefault()
        onClose()
      }}
    >
      <h2 id={titleId}>{title}</h2>
      {children}
      <button className="cancel" onClick={onClose}>
        Done
      </button>
    </dialog>
  )
}

/** Match server reconciliation by deterministic session name and separate lanes. */
export function previewImpact(
  v: LoopStart,
  running: Loop[],
): { kind: string; text: string }[] {
  const quality = v.scope === 'code-quality'
  const project =
    v.repo
      .split('/')
      .filter(Boolean)
      .pop()
      ?.toLowerCase()
      .replace(/[^a-z0-9-]+/g, '-')
      .replace(/^-|-$/g, '') ?? ''
  const names: { name: string; agent: string }[] = []
  const counts: Record<string, number> = {}
  v.agents.forEach((agent) => {
    counts[agent] = (counts[agent] ?? 0) + 1
    names.push({
      name: `loop-${quality ? 'quality-codex' : agent + (counts[agent] > 1 ? counts[agent] : '')}-${project}`,
      agent,
    })
  })
  for (let i = 1; i <= (v.reviews ?? (v.review ? 1 : 0)); i++)
    names.push({
      name: `loop-review${i > 1 ? i : ''}-${project}`,
      agent: 'codex',
    })
  if (v.supervise)
    names.push({
      name: `loop-supervise-${project}`,
      agent: v.superviseAgent ?? 'claude',
    })
  if (v.cleanup)
    names.push({
      name: `loop-cleanup-${project}`,
      agent: v.cleanupAgent ?? 'claude',
    })
  const mine = runningIn(v.repo, running, quality)
  const rows: { kind: string; text: string }[] = []
  for (const plan of names) {
    const collision = running.find(
      (l) => l.name === plan.name && l.repo !== v.repo,
    )
    if (collision) {
      rows.push({
        kind: 'error',
        text: `${plan.name} belongs to ${collision.repo}; the server will reject this change.`,
      })
      continue
    }
    const found = mine.find((l) => l.name === plan.name)
    rows.push({
      kind: !found
        ? 'start'
        : found.agent && found.agent !== plan.agent
          ? 'replace'
          : 'update',
      text: !found
        ? plan.name
        : found.agent && found.agent !== plan.agent
          ? `${plan.name}: provider replacement (requires idle loop).`
          : `${plan.name}: scope, guidance and selected settings.${found.stop ? ' Queued stop cleared.' : ''}`,
    })
  }
  if (!quality)
    mine
      .filter((l) => !names.some((n) => n.name === l.name))
      .forEach((l) =>
        rows.push({
          kind: 'stop',
          text: `${l.name} · ${l.state === 'working' || l.state === 'triaging' ? 'after current task' : 'now'}`,
        }),
      )
  const kept = running.filter(
    (l) =>
      l.repo === v.repo &&
      !names.some((n) => n.name === l.name) &&
      (quality || !mine.includes(l)),
  )
  rows.push({
    kind: 'keep',
    text: kept.length
      ? `${kept.length} other loop${kept.length === 1 ? '' : 's'} in this project keep their assignments and settings.`
      : `${quality ? 'Ordinary' : 'Code-quality'} lane is left alone.`,
  })
  return rows
}
/** The loops a start manages in repo p, running now. The chat supervisor is not one. */
export function runningIn(
  p: string,
  running: Loop[],
  quality: boolean,
): Loop[] {
  return running.filter(
    (l) =>
      l.repo === p &&
      (l.scope === 'code-quality') === quality &&
      (l.role === 'build' ||
        l.role === 'review' ||
        l.role === 'supervise' ||
        l.role === 'cleanup'),
  )
}

/**
 * The form for repo p: what runs there now, so lowering a count stops a loop
 * that is really running. With nothing running, the last start, if it was in
 * this repo.
 */
export function formFor(p: string, running: Loop[], last: LoopStart | null) {
  const ordinary = runningIn(p, running, false)
  const mine = ordinary.length ? ordinary : runningIn(p, running, true)
  if (mine.length) {
    const builds = mine.filter((l) => l.role === 'build')
    const sup = mine.find((l) => l.role === 'supervise')
    const clean = mine.find((l) => l.role === 'cleanup')
    const ref = builds[0] ?? mine[0]
    return {
      claude: builds.filter((l) => l.agent === 'claude').length,
      models: builds
        .filter((l) => l.agent === 'claude')
        .sort((a, b) =>
          a.name.localeCompare(b.name, undefined, { numeric: true }),
        )
        .map((l) => l.model ?? ''),
      supModel: sup?.model ?? '',
      codex: builds.filter((l) => l.agent === 'codex').length,
      reviews: mine.filter((l) => l.role === 'review').length,
      supervise: !!sup,
      supAgent: (sup?.agent || 'claude') as 'claude' | 'codex',
      cleanup: !!clean,
      cleanAgent: (clean?.agent || 'claude') as 'claude' | 'codex',
      cleanDry: clean ? !!clean.dry : true,
      scope: ref.scope || 'full',
      instr: ref.instructions,
      mode: (ref.instrMode || 'add') as 'add' | 'replace',
    }
  }
  const l = last?.repo === p ? last : null
  const many = (a: string) => l?.agents.filter((x) => x === a).length ?? 0
  return {
    claude: l ? many('claude') : 0,
    models: l
      ? l.agents.flatMap((a, i) =>
          a === 'claude' ? [l.models?.[i] ?? ''] : [],
        )
      : ['opus'],
    supModel: l ? (l.superviseModel ?? '') : 'opus',
    codex: l ? many('codex') : 1,
    reviews: l ? (l.reviews ?? (l.review ? 1 : 0)) : 0,
    supervise: l ? !!l.supervise : false,
    supAgent: l?.superviseAgent ?? ('claude' as const),
    cleanup: !!l?.cleanup,
    cleanAgent: l?.cleanupAgent ?? ('claude' as const),
    // A first cleanup only reports, so its first sweep can be read before anything goes.
    cleanDry: l?.cleanup ? !!l.cleanupDry : true,
    scope: l?.scope ?? 'full',
    instr: l?.instructions ?? '',
    mode: l?.instrMode ?? ('add' as const),
  }
}

/** How many build loops of one agent: 0 to 5, each its own session. */
function Stepper({
  name,
  value,
  onChange,
}: {
  name: string
  value: number
  onChange: (n: number) => void
}) {
  return (
    <div className="stepper-row">
      <span className="grow">{name}</span>
      <button
        disabled={value <= 0}
        onClick={() => onChange(value - 1)}
        aria-label={`Fewer ${name} loops`}
      >
        −
      </button>
      <span className="n">{value}</span>
      <button
        disabled={value >= 5}
        onClick={() => onChange(value + 1)}
        aria-label={`More ${name} loops`}
      >
        +
      </button>
    </div>
  )
}

/** Names identify saved guidance; scope alone identifies direct scope choices. */
export function scopeDraftKey(target: string, scope: string, presetName?: string) {
  return JSON.stringify([
    target,
    presetName === undefined ? 'scope' : 'preset',
    presetName ?? scope,
  ])
}

export function scopeSelection(
  target: string,
  scope: string,
  fallback: Form,
  drafts: Map<string, Form>,
  preset?: Preset,
  defaultPreset?: Preset,
): Form {
  const draft = drafts.get(scopeDraftKey(target, scope, preset?.name))
  const saved = preset ?? defaultPreset
  return {
    ...(draft ?? fallback),
    scope: draft?.scope ?? scope,
    presetName: preset?.name,
    ...(!draft && saved ? { instr: saved.instructions, mode: saved.instrMode } : {}),
  }
}
