import { useState } from 'react'
import type { Pane } from '../types'
import { dotClass } from '../types'
import type { GitHubSnapshot, Inbox, InboxItem, Repo } from './types'
import { age, loaded } from './types'
import { EndNote, GhGroup, InboxRow } from './rows'
import { SkeletonCards, SkeletonRows } from './Skeleton'

/**
 * The GitHub screen: an overlay at #/gh, exactly like Settings.
 *
 * It is an overlay and not a fifth tab bar because a tab bar costs about 56px
 * of vertical space on every screen in the app, and pane output is the reason
 * remux exists. One pinned row at the top of the drawer costs nothing anywhere
 * else.
 */
export function GitHubScreen({
  snap,
  loading,
  panes,
  onBack,
  onRefresh,
  onOpenRepo,
  onOpenItem,
  onOpenPane,
  onAdd,
}: {
  snap: GitHubSnapshot
  loading: boolean
  panes: Map<string, Pane>
  onBack: () => void
  onRefresh: () => void
  onOpenRepo: (repo: string) => void
  onOpenItem: (item: InboxItem) => void
  onOpenPane: (p: Pane) => void
  onAdd: () => void
}) {
  const [tab, setTab] = useState<'inbox' | 'repos'>('inbox')
  // Defended rather than assumed: these come off the wire, and a field that
  // is absent must degrade to an empty screen, never to a crash that takes
  // the whole app down.
  const inbox = snap.inbox ?? { needsYou: [], assigned: [], yourPRs: [], replies: 0 }
  const repos = snap.repos ?? []
  // Nothing has been fetched yet - not even from the cache. Every count and
  // every empty state below would be a claim about data that does not exist.
  const cold = !loaded(snap)

  // gh being logged out is the one failure the phone cannot do anything
  // about, so it gets a screen of its own instead of a banner.
  if (snap.errorKind === 'auth' || snap.errorKind === 'nogh') {
    return <NoGh kind={snap.errorKind} onBack={onBack} />
  }

  const total = inbox.needsYou.length + inbox.assigned.length + inbox.yourPRs.length

  return (
    <div className="screen on gh-screen">
      <div className="screen-head">
        <button className="iconbtn" onClick={onBack} aria-label="Back">
          ←
        </button>
        <h2>
          GitHub
          <small>
            {snap.viewer ?? '…'} ·{' '}
            {cold ? 'reading…' : `${repos.length} repo${repos.length === 1 ? '' : 's'} watched`}
          </small>
        </h2>
        <button className={`iconbtn ${loading ? 'spin' : ''}`} onClick={onRefresh} aria-label="Refresh">
          ⟳
        </button>
      </div>

      {loaded(snap) && snap.errorKind && (
        <div className="stale-bar">
          <span className="dot stale" />
          <span className="grow">
            {snap.errorKind === 'offline'
              ? `Cached · GitHub last read ${age(snap.at)} ago`
              : snap.errorKind === 'ratelimit'
                ? 'Rate limited · backing off for a few minutes'
                : `Cached · ${snap.error}`}
          </span>
          <span style={{ color: 'var(--ov1)', fontSize: 11 }} role="button" onClick={onRefresh}>
            Retry
          </span>
        </div>
      )}

      <div className="gh-seg">
        <button className={tab === 'inbox' ? 'on' : ''} onClick={() => setTab('inbox')}>
          Inbox <span className="n">{cold ? '·' : total}</span>
        </button>
        <button className={tab === 'repos' ? 'on' : ''} onClick={() => setTab('repos')}>
          Repos <span className="n">{cold ? '·' : repos.length}</span>
        </button>
      </div>

      {snap.panesBlocked && <PanesBlockedNote />}

      {tab === 'inbox' ? (
        <InboxTab
          inbox={inbox}
          cold={cold}
          panes={panes}
          onOpenItem={onOpenItem}
          onOpenPane={onOpenPane}
        />
      ) : (
        <ReposTab repos={repos} cold={cold} panes={panes} onOpenRepo={onOpenRepo} onAdd={onAdd} />
      )}
    </div>
  )
}

/**
 * Pane chips are missing and there is a reason, so say it.
 *
 * Without this the repo rows just quietly lack their "↗ saas · win 3" and
 * nothing anywhere suggests that one settings toggle would bring them back.
 */
function PanesBlockedNote() {
  return (
    <div className="scopenote warn">
      Pane links are off. macOS is not letting remux see which repo each pane is
      in. Fix it once at the laptop: <b>System Settings → Privacy &amp; Security
      → Full Disk Access</b>, add <code>~/.local/bin/remux</code>, then run{' '}
      <code>remux restart</code>.
    </div>
  )
}

function InboxTab({
  inbox,
  cold,
  panes,
  onOpenItem,
  onOpenPane,
}: {
  inbox: Inbox
  cold: boolean
  panes: Map<string, Pane>
  onOpenItem: (item: InboxItem) => void
  onOpenPane: (p: Pane) => void
}) {
  const { needsYou, assigned, yourPRs, replies } = inbox
  const empty = !needsYou.length && !assigned.length && !yourPRs.length

  if (cold) {
    return (
      <div className="screen-body">
        <SkeletonRows n={5} />
      </div>
    )
  }

  const section = (title: string, items: InboxItem[], hot = false) =>
    items.length > 0 && (
      <>
        <GhGroup title={title} n={items.length} hot={hot} />
        {items.map((it) => (
          <InboxRow
            key={`${it.repo}#${it.number}#${it.kind}`}
            item={it}
            byId={panes}
            onOpen={() => onOpenItem(it)}
            onOpenPane={onOpenPane}
          />
        ))}
      </>
    )

  if (empty) {
    return (
      <div className="screen-body">
        <div className="msg">
          <div className="glyph" style={{ color: 'var(--green)' }}>
            ✓
          </div>
          <h2>Nothing waiting</h2>
          <p>
            No red checks, no review requests, no issues assigned to you.
            {replies > 0 && ` ${replies} replies on threads you opened.`}
          </p>
        </div>
      </div>
    )
  }

  return (
    <div className="screen-body">
      {section('Needs you', needsYou, true)}
      {section('Assigned to you', assigned)}
      {section('Your open PRs', yourPRs)}

      {/* Kevin had 15 unread notifications and 14 of them were replies on his
          own threads. Listing those buries everything that actually needs
          him, so they are one line and a count. */}
      {replies > 0 && (
        <div className="collapse">
          <span className="dot stale" />
          <span>
            {replies} repl{replies === 1 ? 'y' : 'ies'} on threads you opened
          </span>
        </div>
      )}

      <EndNote>Read-only. Nothing here marks a thread read on GitHub.</EndNote>
    </div>
  )
}

function ReposTab({
  repos,
  cold,
  panes,
  onOpenRepo,
  onAdd,
}: {
  repos: Repo[]
  cold: boolean
  panes: Map<string, Pane>
  onOpenRepo: (repo: string) => void
  onAdd: () => void
}) {
  return (
    <div className="screen-body">
      {cold && <SkeletonCards n={4} />}

      {repos.map((r) => (
        <RepoCard key={r.full} repo={r} panes={panes} onOpen={() => onOpenRepo(r.full)} />
      ))}

      {!cold && !repos.length && (
        <div className="msg" style={{ paddingBottom: 8 }}>
          <div className="glyph">◈</div>
          <h2>No repos yet</h2>
          <p>Add the ones you want to keep an eye on. The Inbox works either way.</p>
        </div>
      )}

      <div className="addbar">
        <button className="addbtn" onClick={onAdd}>
          ✚ Add a repo
        </button>
      </div>
    </div>
  )
}

function RepoCard({
  repo,
  panes,
  onOpen,
}: {
  repo: Repo
  panes: Map<string, Pane>
  onOpen: () => void
}) {
  const open = (repo.panes ?? []).map((id) => panes.get(id)).filter((p): p is Pane => !!p)
  const first = open[0]

  // Red beats everything: a blocked pull request is the one thing on this row
  // worth interrupting a scan for. Otherwise the dot mirrors the pane, so the
  // card says the same thing the drawer does about the same work.
  const dot = repo.redPrs ? 'fail' : first ? dotClass(first.status) : 'stale'

  return (
    <button className="repo-card" onClick={onOpen}>
      <span className="av">{repo.name.slice(0, 2).toLowerCase()}</span>
      <span className="mid">
        <span className="nm">
          <span className="owner">{repo.owner}/</span>
          {repo.name}
        </span>
        <span className="st">
          <span className={`dot ${dot}`} />
          {repo.missing ? (
            <span style={{ color: 'var(--peach)' }}>not found on GitHub</span>
          ) : (
            <>
              <b>{repo.issues}</b> issue{repo.issues === 1 ? '' : 's'}{' '}
              {repo.prs ? (
                <>
                  <b>{repo.prs}</b> PR{repo.prs === 1 ? '' : 's'}
                </>
              ) : (
                'no open PRs'
              )}
              {repo.myPrs ? (
                <span style={{ color: repo.redPrs ? 'var(--red)' : 'var(--ov1)' }}>
                  {' · '}
                  {repo.myPrs} of yours
                  {repo.redPrs ? `, ${repo.redPrs} red` : ''}
                </span>
              ) : null}
              {first && (
                <span style={{ color: 'var(--blue)' }}>
                  {' · ↗ '}
                  {first.sessionName} · win {first.windowIndex}
                </span>
              )}
            </>
          )}
        </span>
      </span>
      <span className="chev">›</span>
    </button>
  )
}

/**
 * The only failure the phone cannot fix.
 *
 * There is nothing to paste here on purpose: remux reads GitHub through the
 * gh CLI on the Mac, so the token never leaves the laptop and losing the
 * phone leaks nothing.
 */
function NoGh({ kind, onBack }: { kind: string; onBack: () => void }) {
  return (
    <div className="screen on gh-screen">
      <div className="screen-head">
        <button className="iconbtn" onClick={onBack} aria-label="Back">
          ←
        </button>
        <h2>GitHub</h2>
      </div>
      <div className="msg">
        <div className="glyph warn">◈</div>
        <h2>{kind === 'nogh' ? 'gh is not installed' : 'gh is not logged in'}</h2>
        <p>
          remux reads GitHub through the <code>gh</code> CLI already on your Mac.
          {kind === 'nogh' ? ' It is not on the PATH.' : ' It found no token.'}
        </p>
        <div className="checklist">
          <div>
            <span>›</span>
            <span>
              {kind === 'nogh' ? (
                <>
                  Install it at the laptop: <code>brew install gh</code>.
                </>
              ) : (
                <>
                  Run <code>gh auth login</code> at the laptop once.
                </>
              )}
            </span>
          </div>
          <div>
            <span>›</span>
            <span>Nothing to paste here. No token is stored on this phone.</span>
          </div>
          <div>
            <span>›</span>
            <span>
              Scopes needed: <code>repo</code>, <code>read:org</code>.
            </span>
          </div>
        </div>
        <button className="cta" onClick={onBack}>
          Back
        </button>
      </div>
    </div>
  )
}
