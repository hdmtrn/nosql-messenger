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
    return
  }
  root.setProperty('--app-height', `${visible.height}px`)
  root.setProperty('--app-top', `${visible.offsetTop}px`)
}

visible?.addEventListener('resize', fit)
visible?.addEventListener('scroll', fit)
watch(phone, fit)
fit()
