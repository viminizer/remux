// Tests for the ANSI -> HTML converter.
//
// Run with: npm test
// ansi.ts is transformed by esbuild (already present via Vite) into a temp
// module, so this needs no test-runner dependency of its own.

import test from 'node:test'
import assert from 'node:assert/strict'
import { ansiToHtml, escapeHtml } from '../.test-build/ansi.js'

test('plain text passes through', () => {
  assert.equal(ansiToHtml('hello'), 'hello')
})

test('pane content can never inject markup', () => {
  // The whole safety property of the converter: text is escaped, and the only
  // tags in the output are ones it built itself.
  const evil = '<script>alert(1)</script>'
  const out = ansiToHtml(evil)
  assert.ok(!out.includes('<script>'), out)
  assert.equal(out, '&lt;script&gt;alert(1)&lt;/script&gt;')
})

test('escapes inside a styled span too', () => {
  const out = ansiToHtml('\x1b[31m<b>bad</b>\x1b[0m')
  assert.ok(!out.includes('<b>bad'), out)
  assert.ok(out.includes('&lt;b&gt;bad&lt;/b&gt;'), out)
})

test('16-colour SGR', () => {
  const out = ansiToHtml('\x1b[32mgreen\x1b[0m')
  assert.match(out, /<span style="color:#a6e3a1">green<\/span>/)
})

test('bright colours use the upper eight', () => {
  const out = ansiToHtml('\x1b[91mbright\x1b[0m')
  assert.match(out, /color:#f38ba8/)
})

test('256-colour cube', () => {
  // 39 -> cube index 23 -> r=0, g=175, b=255, xterm's DeepSkyBlue1.
  const out = ansiToHtml('\x1b[38;5;39mc\x1b[0m')
  assert.match(out, /color:#00afff/)
})

test('256-colour greyscale ramp', () => {
  const out = ansiToHtml('\x1b[38;5;244mg\x1b[0m')
  assert.match(out, /color:#808080/)
})

test('truecolor', () => {
  const out = ansiToHtml('\x1b[38;2;18;52;86mt\x1b[0m')
  assert.match(out, /color:#123456/)
})

test('attributes: bold, dim, italic, underline', () => {
  assert.match(ansiToHtml('\x1b[1mb\x1b[0m'), /font-weight:700/)
  assert.match(ansiToHtml('\x1b[2md\x1b[0m'), /opacity:\.65/)
  assert.match(ansiToHtml('\x1b[3mi\x1b[0m'), /font-style:italic/)
  assert.match(ansiToHtml('\x1b[4mu\x1b[0m'), /text-decoration:underline/)
})

test('inverse swaps foreground and background', () => {
  const out = ansiToHtml('\x1b[31;7minv\x1b[0m')
  assert.match(out, /background:#f38ba8/)
})

test('reset clears everything', () => {
  const out = ansiToHtml('\x1b[1;31mred\x1b[0mplain')
  assert.ok(out.endsWith('plain'), out)
})

test('22 clears bold and dim but keeps colour', () => {
  const out = ansiToHtml('\x1b[31;1ma\x1b[22mb')
  const parts = out.match(/<span style="([^"]+)"/g)
  assert.ok(parts.length === 2, out)
  assert.ok(!parts[1].includes('font-weight'), out)
  assert.ok(parts[1].includes('#f38ba8'), out)
})

test('OSC 8 hyperlinks become real anchors', () => {
  const out = ansiToHtml('\x1b]8;;https://example.com/x\x07click\x1b]8;;\x07')
  assert.match(out, /<a href="https:\/\/example\.com\/x" target="_blank" rel="noreferrer noopener">click<\/a>/)
})

test('OSC 8 terminated by ST rather than BEL', () => {
  const out = ansiToHtml('\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\')
  assert.match(out, /<a href="https:\/\/example\.com"/)
})

test('a javascript: URL is not turned into a link', () => {
  // Pane content is untrusted; only http(s) and mailto become anchors.
  const out = ansiToHtml('\x1b]8;;javascript:alert(1)\x07x\x1b]8;;\x07')
  assert.ok(!out.includes('<a '), out)
  assert.ok(!out.includes('javascript:'), out)
})

test('a quote in the URL cannot break out of the attribute', () => {
  const out = ansiToHtml('\x1b]8;;https://e.com/"onmouseover="alert(1)\x07x\x1b]8;;\x07')
  assert.ok(!out.includes('onmouseover="alert'), out)
  assert.ok(out.includes('&quot;'), out)
})

test('cursor and erase sequences are stripped, not printed', () => {
  const out = ansiToHtml('\x1b[2J\x1b[1;1Hclean\x1b[K')
  assert.equal(out, 'clean')
})

test('an unterminated escape does not hang or leak', () => {
  const out = ansiToHtml('text\x1b[')
  assert.ok(!out.includes('\x1b'), JSON.stringify(out))
})

test('box drawing and emoji survive', () => {
  const out = ansiToHtml('╭─✳ ⏵⏵─╮')
  assert.equal(out, '╭─✳ ⏵⏵─╮')
})

test('escapeHtml covers the ampersand first', () => {
  assert.equal(escapeHtml('&<>"'), '&amp;&lt;&gt;&quot;')
})
