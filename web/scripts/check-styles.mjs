// Fails when the client styles an element inline or sets text in capitals.
// Styles live in classes; the one thing a :style binding may carry is a CSS
// custom property holding a value computed at runtime (a picture's width, an
// album tile's place), which a class then reads. No label is set in capitals,
// whether by CSS, by script, or typed so in a template. Run with plain node,
// from anywhere: node web/scripts/check-styles.mjs
import { readdirSync, readFileSync } from 'node:fs'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const src = fileURLToPath(new URL('../src', import.meta.url))
const problems = []

function files(dir, ext) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) =>
    e.isDirectory() ? files(join(dir, e.name), ext) : ext.some((x) => e.name.endsWith(x)) ? [join(dir, e.name)] : [])
}

// Capitals by CSS or script, and the monospace voice that came with them.
const banned = [
  [/uppercase|small-caps|toUpperCase/g, 'sets text in capitals; write it as it should read'],
  [/--font-mono|--mono-tracking|JetBrains|jetbrains-mono/g, 'brings back the retired monospace font; the client sets everything in Archivo'],
]
for (const file of files(src, ['.vue', '.css', '.js'])) {
  const text = readFileSync(file, 'utf8')
  for (const [pattern, why] of banned) {
    for (const m of text.matchAll(pattern)) {
      const line = text.slice(0, m.index).split('\n').length
      problems.push(`${relative(process.cwd(), file)}:${line}: "${m[0]}" ${why}`)
    }
  }
}

// The keys of the object literals in a binding: whatever stands right after a {
// or a comma inside braces and before a colon. Strings are kept whole, so a
// '--x' key stays one token.
function objectKeys(expr) {
  const tokens = expr.match(/'[^']*'|"[^"]*"|`[^`]*`|[A-Za-z_$][\w$]*|\S/g) || []
  const keys = []
  const braces = []
  let expectKey = false
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i]
    if (t === '{') { braces.push(t); expectKey = true; continue }
    if (t === '}') { braces.pop(); expectKey = false; continue }
    if (t === ',' && braces.length) { expectKey = true; continue }
    if (expectKey && tokens[i + 1] === ':') keys.push(t.replace(/^['"`]|['"`]$/g, ''))
    expectKey = false
  }
  return keys
}

for (const file of files(src, ['.vue'])) {
  const text = readFileSync(file, 'utf8')
  const start = text.indexOf('<template>')
  const end = text.lastIndexOf('</template>')
  if (start < 0) continue
  const template = text.slice(start, end)
  const lineOf = (offset) => text.slice(0, start + offset).split('\n').length
  const where = (offset) => `${relative(process.cwd(), file)}:${lineOf(offset)}`

  for (const m of template.matchAll(/(?<![:\w-])style="/g)) {
    problems.push(`${where(m.index)}: inline style; give the element a class`)
  }
  for (const m of template.matchAll(/(?::|v-bind:)style="([^"]*)"/g)) {
    const expr = m[1]
    for (const key of objectKeys(expr)) {
      if (!key.startsWith('--')) problems.push(`${where(m.index)}: :style sets "${key}"; only --custom properties may be bound`)
    }
    for (const name of expr.match(/[A-Za-z_$][\w$]*[Ss]tyle\b/g) || []) {
      problems.push(`${where(m.index)}: :style binds ${name}; bind the --custom properties a class reads`)
    }
  }
  // Text typed in capitals between tags: a word of three or more capital letters.
  // Comments and mustaches are left out, since neither is shown as written.
  const shown = template.replace(/<!--[\s\S]*?-->/g, (c) => ' '.repeat(c.length))
    .replace(/{{[\s\S]*?}}/g, (c) => ' '.repeat(c.length))
  for (const m of shown.matchAll(/>([^<]*)</g)) {
    for (const w of m[1].matchAll(/\b[A-Z]{3,}\b/g)) {
      problems.push(`${where(m.index + 1 + w.index)}: "${w[0]}" is typed in capitals`)
    }
  }
}

if (problems.length) {
  console.error(problems.join('\n'))
  console.error(`\n${problems.length} problem(s) in the client's styles`)
  process.exit(1)
}
console.log('no inline styles and no capitals in the client')
