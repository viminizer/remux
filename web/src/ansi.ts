// ANSI -> HTML, no dependency.
//
// The captures on this machine are full of truecolor SGR, 256-color, OSC 8
// hyperlinks and box drawing, so this handles those and strips everything
// else. It never builds HTML out of pane content: text is escaped first and
// the converter only ever emits spans and anchors it constructed itself.

const ESC = '\x1b'

// 16 ANSI colours, in the mock's Catppuccin Mocha palette so the terminal and
// the app agree on what "red" looks like.
const BASE16 = [
  '#45475a', '#f38ba8', '#a6e3a1', '#f9e2af',
  '#89b4fa', '#cba6f7', '#94e2d5', '#bac2de',
  '#585b70', '#f38ba8', '#a6e3a1', '#f9e2af',
  '#89b4fa', '#cba6f7', '#94e2d5', '#cdd6f4',
]

type Style = {
  fg?: string
  bg?: string
  bold?: boolean
  dim?: boolean
  italic?: boolean
  underline?: boolean
  inverse?: boolean
}

export function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

/** xterm 256-colour cube to hex. */
function color256(n: number): string {
  if (n < 16) return BASE16[n]
  if (n < 232) {
    const i = n - 16
    const steps = [0, 95, 135, 175, 215, 255]
    const r = steps[Math.floor(i / 36) % 6]
    const g = steps[Math.floor(i / 6) % 6]
    const b = steps[i % 6]
    return rgb(r, g, b)
  }
  const v = 8 + (n - 232) * 10
  return rgb(v, v, v)
}

function rgb(r: number, g: number, b: number): string {
  const h = (v: number) => Math.max(0, Math.min(255, v)).toString(16).padStart(2, '0')
  return `#${h(r)}${h(g)}${h(b)}`
}

/** Apply one SGR sequence's parameters to the running style. */
function applySGR(style: Style, params: number[]): Style {
  const s = { ...style }
  for (let i = 0; i < params.length; i++) {
    const p = params[i]
    if (p === 0) {
      for (const k of Object.keys(s)) delete (s as Record<string, unknown>)[k]
    } else if (p === 1) s.bold = true
    else if (p === 2) s.dim = true
    else if (p === 3) s.italic = true
    else if (p === 4) s.underline = true
    else if (p === 7) s.inverse = true
    else if (p === 22) { s.bold = false; s.dim = false }
    else if (p === 23) s.italic = false
    else if (p === 24) s.underline = false
    else if (p === 27) s.inverse = false
    else if (p >= 30 && p <= 37) s.fg = BASE16[p - 30]
    else if (p === 39) delete s.fg
    else if (p >= 40 && p <= 47) s.bg = BASE16[p - 40]
    else if (p === 49) delete s.bg
    else if (p >= 90 && p <= 97) s.fg = BASE16[p - 90 + 8]
    else if (p >= 100 && p <= 107) s.bg = BASE16[p - 100 + 8]
    else if (p === 38 || p === 48) {
      const target = p === 38 ? 'fg' : 'bg'
      const mode = params[i + 1]
      if (mode === 5) {
        s[target] = color256(params[i + 2] ?? 0)
        i += 2
      } else if (mode === 2) {
        s[target] = rgb(params[i + 2] ?? 0, params[i + 3] ?? 0, params[i + 4] ?? 0)
        i += 4
      }
    }
  }
  return s
}

function styleAttr(s: Style): string {
  const bits: string[] = []
  let fg = s.fg
  let bg = s.bg
  if (s.inverse) {
    fg = s.bg ?? '#1e1e2e'
    bg = s.fg ?? '#cdd6f4'
  }
  if (fg) bits.push(`color:${fg}`)
  if (bg) bits.push(`background:${bg}`)
  if (s.bold) bits.push('font-weight:700')
  if (s.dim) bits.push('opacity:.65')
  if (s.italic) bits.push('font-style:italic')
  if (s.underline) bits.push('text-decoration:underline')
  return bits.join(';')
}

/**
 * Convert captured terminal output to HTML.
 *
 * The output is a string of spans and anchors, all built here; nothing from
 * the pane is ever inserted as markup.
 */
export function ansiToHtml(input: string): string {
  let out = ''
  let style: Style = {}
  let link: string | null = null
  let buf = ''

  const flush = () => {
    if (!buf) return
    const text = escapeHtml(buf)
    const attr = styleAttr(style)
    let piece = attr ? `<span style="${attr}">${text}</span>` : text
    if (link) piece = `<a href="${escapeHtml(link)}" target="_blank" rel="noreferrer noopener">${piece}</a>`
    out += piece
    buf = ''
  }

  let i = 0
  while (i < input.length) {
    const ch = input[i]

    if (ch !== ESC) {
      buf += ch
      i++
      continue
    }

    const next = input[i + 1]

    // CSI: ESC [ params letter
    if (next === '[') {
      let j = i + 2
      while (j < input.length && !/[@-~]/.test(input[j])) j++
      const final = input[j]
      const body = input.slice(i + 2, j)
      if (final === 'm') {
        flush()
        const params = body
          .split(';')
          .map((p) => (p === '' ? 0 : parseInt(p, 10)))
          .map((n) => (Number.isNaN(n) ? 0 : n))
        style = applySGR(style, params)
      }
      // Every other CSI (cursor moves, erases) is dropped: capture-pane
      // hands us an already-rendered screen, so there is nothing to move.
      i = j + 1
      continue
    }

    // OSC: ESC ] ... BEL or ST. Only hyperlinks are kept.
    if (next === ']') {
      let j = i + 2
      while (j < input.length && input[j] !== '\x07' && !(input[j] === ESC && input[j + 1] === '\\')) j++
      const body = input.slice(i + 2, j)
      const term = input[j] === ESC ? 2 : 1

      const m = /^8;[^;]*;(.*)$/.exec(body)
      if (m) {
        flush()
        const url = m[1]
        // An empty URL closes the link.
        link = url && /^(https?|mailto):/i.test(url) ? url : null
      }
      i = j + term
      continue
    }

    // Any other escape: drop the two bytes.
    i += 2
  }

  flush()
  return out
}
