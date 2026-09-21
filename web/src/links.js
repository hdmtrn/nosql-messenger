// What a person wrote is rendered as text and never as HTML — Vue's
// interpolation escapes it, and that is the only reason a message cannot carry
// markup into the page. Making a link clickable therefore means cutting the
// text into pieces and letting the template decide what each piece becomes.

// Deliberately narrow: a scheme we allow, then everything up to whitespace.
// Any other scheme stays plain text, which is the point — "javascript:" in an
// href is a script the reader runs by clicking, and the browser goes by the
// scheme, not by how the text looked.
const URL_RE = /https?:\/\/[^\s]+/gi

const PUNCTUATION = '.,;:!?\'"'
const CLOSING = { ')': '(', ']': '[', '}': '{' }

const count = (s, ch) => s.split(ch).length - 1

// A link at the end of a sentence would swallow the full stop, and one in
// brackets the closing bracket. Both are cut back off — unless the link opened
// that bracket itself, as a Wikipedia address does.
function trimTail(url) {
  while (url) {
    const last = url[url.length - 1]
    const open = CLOSING[last]
    if (PUNCTUATION.includes(last) || (open && count(url, open) < count(url, last))) {
      url = url.slice(0, -1)
      continue
    }
    break
  }
  return url
}

// [{ text }, { text, href }, ...] — the pieces in the order they were written.
export function parts(text) {
  const s = String(text ?? '')
  const out = []
  let at = 0

  for (const m of s.matchAll(URL_RE)) {
    const url = trimTail(m[0])
    if (!url) continue
    if (m.index > at) out.push({ text: s.slice(at, m.index) })
    out.push({ text: url, href: url })
    at = m.index + url.length
  }
  if (at < s.length) out.push({ text: s.slice(at) })
  return out
}

// Whether a link leaves this site. A href we cannot even parse counts as
// foreign: the safe answer is the one that does not keep it in our tab.
export function external(href) {
  try {
    return new URL(href).origin !== location.origin
  } catch {
    return true
  }
}

// The code of an invite link of ours, or null for anything else. Same shape as
// the cold start in pending.js, and the same reason for the try: a link mangled
// in transit can carry an escape sequence decodeURIComponent refuses.
export function inviteCode(href) {
  let url
  try {
    url = new URL(href)
  } catch {
    return null
  }
  if (url.origin !== location.origin) return null

  const match = url.pathname.match(/^\/invite\/(.+)$/)
  if (!match) return null
  try {
    return decodeURIComponent(match[1])
  } catch {
    return null
  }
}
