import { ref, watch } from 'vue'

// Telegram Web A's thresholds (MOBILE_SCREEN_MAX_WIDTH and the landscape pair in
// its config.ts): a phone held upright, or one on its side. The stylesheets
// repeat this query, as CSS cannot import it.
export const PHONE_QUERY = '(max-width: 600px), (max-width: 950px) and (max-height: 450px)'

const query = window.matchMedia(PHONE_QUERY)

// One screen at a time below these sizes; the app lives as long as the page, so
// the listener is never removed.
export const phone = ref(query.matches)
query.addEventListener('change', (e) => { phone.value = e.matches })

// Screens slide in this long, Telegram's --layer-transition.
export const SLIDE_MS = 300

// iOS Safari does not shrink the page for the keyboard: it pans the page up to
// show the field, and the chat's header leaves the screen. A phone's screen is
// pinned to the part above the keyboard instead, as Telegram's windowSize.ts
// does. Not while zoomed in: the visible part is then a magnified piece.
const visible = window.visualViewport

function fit() {
  const root = document.documentElement.style
  if (!visible || !phone.value || Math.abs(visible.scale - 1) > 0.01) {
    root.removeProperty('--app-height')
    root.removeProperty('--app-top')
    return ''
  }
  root.setProperty('--app-height', `${visible.height}px`)
  root.setProperty('--app-top', `${visible.offsetTop}px`)
  return `${visible.height}:${visible.offsetTop}`
}

// The keyboard moves in its own animation, and iOS reports it in a few steps
// that need not include the last: after the keyboard's Done the page can keep
// the keyboard's offset, and the screen stays pushed down. So after any change
// the size is read every frame until it has held still, for a second at most.
const SETTLE_MS = 600
const SETTLE_MAX_MS = 1000
let deadline = 0
let frame = 0
// Set when a field lets go of the keyboard: once it is gone, whatever iOS left
// panned is put back. Not earlier, as moving the page mid-animation jolts it.
let unpan = false

function settle() {
  const now = performance.now()
  deadline = now + SETTLE_MS
  if (frame) return
  const stop = now + SETTLE_MAX_MS
  let last = fit()
  const step = () => {
    const key = fit()
    const t = performance.now()
    if (t < stop && (t < deadline || key !== last)) {
      last = key
      frame = requestAnimationFrame(step)
      return
    }
    frame = 0
    if (unpan && !isField(document.activeElement)) {
      unpan = false
      if (window.scrollY || visible.offsetTop) {
        window.scrollTo(0, 0)
        settle()
      }
    }
  }
  frame = requestAnimationFrame(step)
}

const isField = (el) => !!el && (el.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName))

// Moving on to another field keeps the keyboard, and nothing is put back.
function onFocusOut(e) {
  if (!phone.value || isField(e.relatedTarget)) return
  unpan = true
  settle()
}

visible?.addEventListener('resize', settle)
visible?.addEventListener('scroll', settle)
document.addEventListener('focusin', settle)
document.addEventListener('focusout', onFocusOut)
watch(phone, settle)
fit()
