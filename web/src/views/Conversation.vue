<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import PaneHeader from '../components/PaneHeader.vue'
import MessageBubble from '../components/MessageBubble.vue'
import MessageComposer from '../components/MessageComposer.vue'
import MessageMenu from '../components/MessageMenu.vue'
import MessageText from '../components/MessageText.vue'
import SgAvatar from '../components/SgAvatar.vue'
import ChannelGlyph from '../components/ChannelGlyph.vue'
import MediaViewer from '../components/MediaViewer.vue'
import { avatarUrl, channelAvatarUrl, displayName, initials, messagePreview, otherMember } from '../naming'
import { phone } from '../viewport'

const props = defineProps({
  me: { type: Object, required: true },
  channel: { type: Object, required: true },
  title: { type: String, required: true },
  messages: { type: Array, default: () => [] },
  // username whose page is open beside the feed
  person: { type: String, default: '' },
  infoOpen: Boolean,
  forwardTargets: { type: Array, default: () => [] },
  // { kind: 'reply' | 'forward', message } waiting above the field until Send.
  pending: { type: Object, default: null },
  // Originals of replies fetched because they are not in this page, by id.
  originals: { type: Map, default: () => new Map() },
  // Usernames of the people typing here right now, the viewer never among them.
  typing: { type: Array, default: () => [] },
  // { online, last_seen } of the other person in a direct chat; null elsewhere.
  presence: { type: Object, default: null },
  // Unread in the other chats, on the phone's arrow back to the list.
  backCount: { type: Number, default: 0 },
})

const emit = defineEmits(['send', 'retry', 'discard', 'load-older', 'info', 'person',
  'reply', 'forward', 'cancel-pending', 'find', 'typing', 'invite', 'back'])

const feed = ref(null)
const composer = ref(null)

// Opening a chat puts the cursor in its field, as in Telegram, except on touch
// screens, where focus would pull up the keyboard over the messages.
function focusField() {
  if (window.matchMedia('(hover: hover)').matches) composer.value?.focus()
}
onMounted(focusField)

// The field stays mounted across chats; pictures picked in one must not be sent to another.
watch(() => props.channel.id, () => {
  composer.value?.clearFiles()
  viewing.value = null
  nextTick(focusField)
})

// Every loaded picture of the chat in feed order, so the viewer can flip past the
// message that was clicked. A key names a picture by its message and position.
const pictureKey = (m, i) => `${m.id || m.client_msg_id}:${i}`
const pictures = computed(() => props.messages.flatMap((m) =>
  (m.attachments || []).map((a, i) => ({ key: pictureKey(m, i), url: a.preview || `/media/${a.id}` }))))
// Index in pictures of the one the viewer opened on, or null when it is closed.
const viewing = ref(null)

function openPicture(m, i) {
  const at = pictures.value.findIndex((p) => p.key === pictureKey(m, i))
  if (at >= 0) viewing.value = at
}

// A reply or a forward waiting above the field means the next thing to do is
// type, so the cursor goes there, as in Telegram.
watch(() => props.pending, async (action) => {
  if (!action) return
  await nextTick()
  composer.value?.focus()
})

// The message whose menu is open, and where the menu stands.
const menu = ref(null)

// Only a stored message has an id the server can forward; one still sending or
// failed has nothing to act on yet.
function openMenu(m, x, y) {
  if (m.id) menu.value = { m, x, y }
}

// A forwarded message is shown under its original author, both in a quote and
// in the bar above the field, the way the forward itself names it.
function sourceAuthor(m) {
  return displayName((m.forwarded || m).author.username)
}

const pendingAuthor = computed(() => (props.pending ? sourceAuthor(props.pending.message) : ''))

const placeholder = computed(() => {
  if (props.pending?.kind === 'reply') return `Reply to ${pendingAuthor.value}…`
  if (props.pending?.kind === 'forward') return 'Add a comment…'
  return direct() ? `Message ${displayName(props.title)}…` : `Message #${props.channel.name}…`
})

// The quote of a reply: from the loaded page, else from the fetched originals.
// undefined while it is still being fetched, so nothing jumps in half-drawn.
function quoteOf(m, byId) {
  if (!m.reply_to) return null
  const orig = byId.get(m.reply_to) ?? props.originals.get(m.reply_to)
  if (orig === undefined) return null
  if (orig === null) return { missing: true }
  return { author: sourceAuthor(orig), text: messagePreview(orig) }
}
// A held finger opens the menu: touch screens have no right click, and iOS
// Safari never fires contextmenu. 200 ms, and any move cancels it, as
// Telegram's long tap (LONG_TAP_DURATION_MS), so a scroll never opens it.
const LONG_PRESS_MS = 200
let press = null

function pressStart(m, e) {
  pressCancel()
  if (e.touches.length !== 1 || !m.id) return
  const { clientX, clientY } = e.touches[0]
  const held = { fired: false }
  held.timer = setTimeout(() => {
    held.fired = true
    openMenu(m, clientX, clientY)
  }, LONG_PRESS_MS)
  press = held
}
function pressMove() {
  if (press && !press.fired) pressCancel()
}
function pressCancel() {
  if (press) clearTimeout(press.timer)
  press = null
}
// The lift that ends a long press is not a tap: it must not open the picture or link under the finger.
function pressEnd(e) {
  if (press?.fired) e.preventDefault()
  pressCancel()
}

function openMenuAtBubble(m, e) {
  const box = e.currentTarget.getBoundingClientRect()
  openMenu(m, box.left + 48, box.bottom)
}

async function copyText() {
  const text = menu.value.m.text
  menu.value = null
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    // Clipboard access can be refused by the browser; there is nothing to retry.
  }
}

function reply() {
  const m = menu.value.m
  menu.value = null
  emit('reply', m)
}

function forward(channelId) {
  const m = menu.value.m
  menu.value = null
  emit('forward', m, channelId)
}

const direct = () => props.channel.kind === 'direct'

// Typing takes the place of the subtitle, as in Telegram. A direct conversation
// has one other person, so their name would only repeat the title.
const typingLine = computed(() => {
  const names = props.typing.map(displayName)
  if (!names.length) return ''
  if (direct()) return 'typing…'
  if (names.length === 1) return `${names[0]} is typing…`
  if (names.length === 2) return `${names[0]} and ${names[1]} are typing…`
  return `${names.length} people are typing…`
})

// "5 minutes ago" would otherwise freeze at what it said when the event came.
const now = ref(Date.now())
const ticker = setInterval(() => { now.value = Date.now() }, 60_000)
onUnmounted(() => { clearInterval(ticker); clearTimeout(idleTimer) })

function lastSeenLine(iso) {
  const at = new Date(iso)
  const minutes = Math.floor((now.value - at.getTime()) / 60_000)
  if (minutes < 1) return 'last seen just now'
  if (minutes < 60) return `last seen ${minutes} minute${minutes === 1 ? '' : 's'} ago`

  const time = at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  const midnight = new Date(now.value)
  midnight.setHours(0, 0, 0, 0)
  if (at >= midnight) return `last seen at ${time}`
  if (at >= new Date(midnight.getTime() - 86_400_000)) return `last seen yesterday at ${time}`
  return `last seen on ${at.toLocaleDateString([], { day: 'numeric', month: 'short' })}`
}

// Telegram's order: typing wins over presence, presence over the handle.
const presenceLine = computed(() => {
  if (!direct() || !props.presence) return ''
  if (props.presence.online) return 'online'
  return props.presence.last_seen ? lastSeenLine(props.presence.last_seen) : ''
})

// A pause this long ends a run even when the same person keeps talking: the
// header is what tells the reader the conversation moved on in time.
const RUN_BREAK_MS = 5 * 60 * 1000

// Consecutive messages from one author collapse into a run, and only its first
// message carries the avatar, the name and the clock. A new day starts a run too,
// under its own date.
const rows = computed(() => {
  const byId = new Map(props.messages.map((m) => [m.id, m]))
  return props.messages.map((m, i) => {
    const prev = props.messages[i - 1]
    const day = !prev || dayKey(prev.created_at) !== dayKey(m.created_at) ? dayLabel(m.created_at) : ''
    const head =
      !!day ||
      prev.author.id !== m.author.id ||
      new Date(m.created_at) - new Date(prev.created_at) > RUN_BREAK_MS
    return { m, head, day, quote: quoteOf(m, byId) }
  })
})

function clock(iso) {
  // 24-hour whatever the locale: "18:25" is a third narrower than "06:25 PM"
  return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
}

// A message still being sent has no server time yet; it belongs to today.
const dayKey = (iso) => (iso ? new Date(iso) : new Date()).toDateString()

// As Telegram writes it: "October 12", and with the year once it is not this one.
function dayLabel(iso) {
  const at = iso ? new Date(iso) : new Date()
  const year = at.getFullYear() === new Date().getFullYear() ? undefined : 'numeric'
  return at.toLocaleDateString('en', { month: 'long', day: 'numeric', year })
}

// The date of the day at the top of the feed floats over it while someone
// scrolls it, once that day's own separator has gone above the edge, and fades
// when it stops. Only a hand counts: the jump to the bottom on opening a chat
// is a scroll too, and the date would flash over the messages.
const floatingDay = ref('')
const scrolling = ref(false)
let idleTimer = null
let byHand = false
const onHand = () => { byHand = true }

function updateFloatingDay() {
  const top = feed.value.scrollTop
  let label = ''
  for (const el of feed.value.querySelectorAll('.day')) {
    if (el.offsetTop >= top) break
    label = el.textContent.trim()
  }
  floatingDay.value = label
}

function onScroll() {
  if (!feed.value) return
  if (feed.value.scrollTop < 80) emit('load-older')
  atBottom = distanceFromBottom() - feed.value.clientHeight < AT_BOTTOM_PX
  if (!byHand) return
  updateFloatingDay()
  scrolling.value = true
  clearTimeout(idleTimer)
  idleTimer = setTimeout(() => { scrolling.value = false; byHand = false }, 1000)
}

// A feed read at its newest message stays there when it gets shorter, as the
// keyboard comes up, or taller: Telegram keeps the last line above the field.
const AT_BOTTOM_PX = 40
let atBottom = true
const distanceFromBottom = () => (feed.value ? feed.value.scrollHeight - feed.value.scrollTop : 0)
const feedSize = new ResizeObserver(() => {
  if (atBottom && feed.value) feed.value.scrollTop = feed.value.scrollHeight
})
onMounted(() => feedSize.observe(feed.value))
onUnmounted(() => feedSize.disconnect())

// The pane owns the scroll box, so it owns keeping the view at the bottom.
async function toBottom() {
  await nextTick()
  if (feed.value) feed.value.scrollTop = feed.value.scrollHeight
  atBottom = true
}

// Restores the reading position after older messages are prepended above.
async function keepPosition(before) {
  await nextTick()
  if (feed.value) feed.value.scrollTop = feed.value.scrollHeight - before
}

watch(() => props.channel.id, toBottom)

// The message a quote led to is lit for a moment, so the eye finds it.
const lit = ref('')
let litTimer = null

async function highlight(id) {
  await nextTick()
  const el = feed.value?.querySelector(`[data-id="${id}"]`)
  if (!el) return
  el.scrollIntoView({ block: 'center', behavior: 'smooth' })
  lit.value = id
  clearTimeout(litTimer)
  litTimer = setTimeout(() => { lit.value = '' }, 1200)
}

function openQuote(m) {
  if (props.messages.some((x) => x.id === m.reply_to)) highlight(m.reply_to)
  else emit('find', m.reply_to)
}

defineExpose({ highlight, toBottom, keepPosition, distanceFromBottom })
</script>

<template>
  <section class="conversation">
    <PaneHeader
      :title="direct() ? displayName(title) : title"
      :subtitle="typingLine || presenceLine || (direct() ? (otherMember(channel, me.id) ? '@' + title : '')
                          : channel.member_count + (channel.member_count === 1 ? ' member' : ' members'))"
      :subtitle-accent="!!typingLine || presence?.online === true"
      :open="infoOpen"
      :back="phone"
      :back-count="backCount"
      @info="emit('info')"
      @back="emit('back')"
    >
      <template #mark>
        <SgAvatar v-if="direct()" :initials="initials(displayName(title))" :src="avatarUrl(title)" :size="phone ? 40 : 44" />
        <ChannelGlyph v-else :src="channelAvatarUrl(channel)" :size="phone ? 40 : 44" tone="blue" />
      </template>
    </PaneHeader>

    <div class="feed-box">
    <div ref="feed" class="feed" @scroll="onScroll" @wheel.passive="onHand" @touchstart.passive="onHand"
         @pointerdown="onHand" @keydown="onHand">
      <p v-if="!messages.length" class="empty">
        No messages yet
      </p>

      <template v-for="({ m, head, day, quote }, i) in rows" :key="m.id || m.client_msg_id">
      <div v-if="day" class="day"><span class="day-pill">{{ day }}</span></div>
      <MessageBubble
        :own="m.author.id === me.id"
        :head="head"
        :avatar="!direct()"
        :status="m.status || 'delivered'"
        :author="direct() ? '' : displayName(m.author.username)"
        :initials="initials(displayName(m.author.username))"
        :avatar-src="avatarUrl(m.author.username)"
        :attachments="m.attachments"
        :time="m.status && m.status !== 'delivered' ? '' : clock(m.created_at)"
        :ring="!!person && m.author.username === person"
        :forwarded="m.forwarded ? displayName(m.forwarded.author.username) : ''"
        :quote="quote"
        @retry="emit('retry', m)"
        @discard="emit('discard', m)"
        :tabindex="m.id ? 0 : undefined"
        class="message"
        :class="{ lit: lit && lit === m.id, 'run-start': head && i > 0 && !day, tight: direct() }"
        :data-id="m.id"
        @contextmenu.prevent="openMenu(m, $event.clientX, $event.clientY)"
        @touchstart.passive="pressStart(m, $event)"
        @touchmove.passive="pressMove"
        @touchend="pressEnd"
        @touchcancel="pressCancel"
        @keydown.enter.self="openMenuAtBubble(m, $event)"
        @author="emit('person', m.author.username)"
        @quote="openQuote(m)"
        @picture="openPicture(m, $event)"
        @forwarded-author="m.forwarded.author.id === me.id || emit('person', m.forwarded.author.username)"
      ><MessageText :text="m.text" @invite="emit('invite', $event)" /></MessageBubble>
      </template>
    </div>
    <div class="day floating" :class="{ shown: scrolling && floatingDay }" aria-hidden="true">
      <span class="day-pill">{{ floatingDay }}</span>
    </div>
    </div>

    <MediaViewer
      v-if="viewing !== null"
      :pictures="pictures"
      :start="viewing"
      @close="viewing = null"
    />

    <MessageMenu
      v-if="menu"
      :x="menu.x"
      :y="menu.y"
      :targets="forwardTargets"
      @reply="reply"
      @copy="copyText"
      @forward="forward"
      @close="menu = null"
    />

    <MessageComposer
      ref="composer"
      :placeholder="placeholder"
      :ready="pending?.kind === 'forward'"
      @send="(text, attachments) => emit('send', text, attachments)"
      @typing="emit('typing')"
    >
      <!-- a reply still needs its own text; a forward can go on its own -->
      <div v-if="pending" class="pending">
        <span class="bar" />
        <div class="pending-body">
          <span class="pending-label">
            {{ pending.kind === 'reply' ? 'Reply to' : 'Forward from' }} {{ pendingAuthor }}
          </span>
          <span class="pending-text">{{ messagePreview(pending.message) }}</span>
        </div>
        <button type="button" class="cancel"
                :aria-label="pending.kind === 'reply' ? 'Cancel reply' : 'Cancel forward'"
                @click="emit('cancel-pending')">×</button>
      </div>
    </MessageComposer>
  </section>
</template>

<style scoped>
.conversation {
  flex: 1;
  min-width: 0;
  background: var(--surface-panel);
  border-radius: var(--radius-panel);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.empty {
  margin: auto;
  font: var(--text-meta);
  color: var(--text-muted);
}
.feed {
  flex: 1;
  overflow-y: auto;
  padding: 22px 28px;
  display: flex;
  flex-direction: column;
  /* spacing inside a run; the first bubble of a run adds its own margin on top */
  gap: 4px;
}
/* The feed scrolls inside this box, which holds the floating date still over it. */
.feed-box {
  flex: 1;
  min-height: 0;
  position: relative;
  display: flex;
  flex-direction: column;
}
.day {
  display: flex;
  justify-content: center;
  margin: 18px 0 6px;
}
.day-pill {
  padding: 4px 12px;
  border-radius: var(--radius-pill);
  background: var(--surface-sunken);
  color: var(--text-muted);
  font: var(--text-meta);
}
.day.floating {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  margin: 10px 0 0;
  pointer-events: none;
  opacity: 0;
  transition: opacity 0.3s ease;
  z-index: 1;
}
.day.floating.shown { opacity: 1; }
/* short conversations hug the bottom; long ones still scroll from the top */
.feed > :first-child { margin-top: auto; }
.message { border-radius: var(--radius-bubble); }
/* the first bubble of a run stands apart from the run above it, less so in a
   direct conversation, where the side already says who is speaking */
.message.run-start { margin-top: 18px; }
.message.run-start.tight { margin-top: 10px; }
/* The band a quote leads to spans the whole pane, past the feed's 28px padding,
   with room above and below. As in Telegram it lies over the message, a see-through
   wash of the accent, so a blue bubble is lit too; clicks go through it. */
.message { position: relative; }
.message::before {
  content: '';
  position: absolute;
  inset: -8px -28px;
  z-index: 1;
  background: rgba(26, 24, 229, 0.12);
  opacity: 0;
  transition: opacity 300ms linear;
  pointer-events: none;
}
.message.lit::before { opacity: 1; }
.message:focus-visible { outline: 2px solid var(--focus-ring); outline-offset: 2px; }
.pending {
  display: flex;
  align-items: center;
  gap: 14px;
  margin-bottom: 12px;
  padding: 10px 12px 10px 16px;
  background: var(--panel-tint);
  border-radius: var(--radius-panel);
}
.bar {
  align-self: stretch;
  width: 2px;
  background: var(--blue);
  border-radius: 2px;
}
.pending-body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.pending-label { font: 500 13px/1.3 var(--font-ui); color: var(--blue); }
.pending-text {
  font: var(--text-body);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.cancel {
  flex: none;
  width: 32px;
  height: 32px;
  border: none;
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--text-muted);
  font: 400 22px/1 var(--font-ui);
  cursor: pointer;
}
.cancel:hover { background: var(--surface-panel); color: var(--text-primary); }
/* A finger held on a bubble opens our menu; the browser's text selection and
   its own picture menu would open on top of it. Copy text is in our menu. */
@media (pointer: coarse) {
  .message {
    -webkit-touch-callout: none;
    -webkit-user-select: none;
    user-select: none;
  }
  .cancel { width: 48px; height: 48px; }
}
/* Edge to edge, with Telegram's 8px side inset for the feed (--middle-panel-inline-padding). */
@media (max-width: 600px), (max-width: 950px) and (max-height: 450px) {
  .conversation { border-radius: 0; }
  .feed { padding: 12px max(8px, env(safe-area-inset-right)) 12px max(8px, env(safe-area-inset-left)); }
  .message::before { inset: -8px -8px; }
  .pending { gap: 8px; margin-bottom: 8px; padding: 4px 4px 4px 12px; }
}
</style>
