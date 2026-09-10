/**
 * Placeholders shaped like the rows that are coming.
 *
 * A centred "Loading…" said very little on these screens. The list is the
 * page, so a blank page with one word in the middle reads as "there is
 * nothing here" - and on the GitHub screen it was worse than that, because an
 * unfetched snapshot is indistinguishable from an empty one and the screen
 * cheerfully drew "Nothing waiting" over data nobody had asked for yet.
 *
 * Rows in the right shape say "this is filling in" without a word, and the
 * page does not jump when the real ones replace them.
 *
 * The widths vary per row on purpose. Identical bars read as a table or a
 * broken render; ragged ones read as text.
 */

const TITLE = ['72%', '54%', '81%', '46%', '66%', '58%']
const SUB = ['38%', '52%', '30%', '44%', '35%', '48%']

/** One shimmering block. `w` is a CSS width, `h` a pixel height. */
function Bar({ w, h = 10 }: { w: string; h?: number }) {
  return <span className="sk" style={{ width: w, height: h }} />
}

/** A row is staggered so the group reads as one wave rather than a strobe. */
function delay(i: number) {
  return { animationDelay: `${i * 90}ms` }
}

/** Inbox and issue/PR rows: a kind square, two lines, an age stamp. */
export function SkeletonRows({ n = 4 }: { n?: number }) {
  return (
    <div aria-busy="true" aria-label="Loading">
      {Array.from({ length: n }, (_, i) => (
        <div className="gh skrow" key={i} style={delay(i)}>
          <span className="gh-hit">
            <span className="kind sk" />
            <span className="mid">
              <Bar w={SUB[i % SUB.length]} h={8} />
              <Bar w={TITLE[i % TITLE.length]} h={11} />
              <Bar w="30%" h={8} />
            </span>
            <span className="age">
              <Bar w="18px" h={8} />
            </span>
          </span>
        </div>
      ))}
    </div>
  )
}

/** Repo cards: a rounded avatar and two lines. */
export function SkeletonCards({ n = 3 }: { n?: number }) {
  return (
    <div aria-busy="true" aria-label="Loading">
      {Array.from({ length: n }, (_, i) => (
        <div className="repo-card skrow" key={i} style={delay(i)}>
          <span className="av sk" />
          <span className="mid">
            <Bar w={TITLE[i % TITLE.length]} h={11} />
            <Bar w={SUB[i % SUB.length]} h={9} />
          </span>
        </div>
      ))}
    </div>
  )
}

/** Picker rows in the Add a repo sheet. */
export function SkeletonPicks({ n = 5 }: { n?: number }) {
  return (
    <div aria-busy="true" aria-label="Loading">
      {Array.from({ length: n }, (_, i) => (
        <div className="pick skrow" key={i} style={delay(i)}>
          <span className="av sk" />
          <span className="mid">
            <Bar w={TITLE[i % TITLE.length]} h={11} />
            <Bar w={SUB[i % SUB.length]} h={8} />
          </span>
        </div>
      ))}
    </div>
  )
}

/**
 * The head of one issue or pull request.
 *
 * This screen was the emptiest of them all while it loaded: a bare "#41" with
 * no title, no body and no thread, which looks exactly like an issue somebody
 * filed blank.
 */
export function SkeletonItem() {
  return (
    <div aria-busy="true" aria-label="Loading">
      <div className="det-head skrow">
        <Bar w="34%" h={9} />
        <span style={{ display: 'block', height: 10 }} />
        <Bar w="88%" h={15} />
        <Bar w="61%" h={15} />
        <span style={{ display: 'block', height: 8 }} />
        <Bar w="45%" h={18} />
      </div>
      <div className="det-sec skrow">
        <Bar w="24%" h={9} />
        <span style={{ display: 'block', height: 10 }} />
        <Bar w="100%" h={9} />
        <Bar w="96%" h={9} />
        <Bar w="72%" h={9} />
      </div>
    </div>
  )
}
