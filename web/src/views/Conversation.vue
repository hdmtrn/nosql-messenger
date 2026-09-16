<script setup>
import { computed, nextTick, ref, watch } from 'vue'
import PaneHeader from '../components/PaneHeader.vue'
import MessageBubble from '../components/MessageBubble.vue'
import MessageComposer from '../components/MessageComposer.vue'
import MessageMenu from '../components/MessageMenu.vue'
import SgAvatar from '../components/SgAvatar.vue'
import ChannelGlyph from '../components/ChannelGlyph.vue'
import MediaViewer from '../components/MediaViewer.vue'
import { avatarUrl, displayName, initials, messagePreview } from '../naming'

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
})

const emit = defineEmits(['send', 'retry', 'discard', 'load-older', 'info', 'person',
  'reply', 'forward', 'cancel-pending', 'find'])

const feed = ref(null)
const composer = ref(null)

// A reply or a forward waiting above the field means the next thing to do is
// type, so the cursor goes there, as in Telegram.
// The field stays mounted across chats; pictures picked in one must not be sent to another.
watch(() => props.channel.id, () => {
  composer.value?.clearFiles()
  viewing.value = null
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

// A pause this long ends a run even when the same person keeps talking: the
// header is what tells the reader the conversation moved on in time.
const RUN_BREAK_MS = 5 * 60 * 1000

// Consecutive messages from one author collapse into a run, and only its first
// message carries the avatar, the name and the clock.
const rows = computed(() => {
  const byId = new Map(props.messages.map((m) => [m.id, m]))
  return props.messages.map((m, i) => {
    const prev = props.messages[i - 1]
    const head =
      !prev ||
      prev.author.id !== m.author.id ||
      new Date(m.created_at) - new Date(prev.created_at) > RUN_BREAK_MS
    return { m, head, quote: quoteOf(m, byId) }
  })
})

function clock(iso) {
  // 24-hour whatever the locale: "18:25" is a third narrower than "06:25 PM"
  return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
}

function onScroll() {
  if (feed.value && feed.value.scrollTop < 80) emit('load-older')
}

// The pane owns the scroll box, so it owns keeping the view at the bottom.
async function toBottom() {
  await nextTick()
  if (feed.value) feed.value.scrollTop = feed.value.scrollHeight
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

defineExpose({ highlight, toBottom, keepPosition, distanceFromBottom: () => (feed.value ? feed.value.scrollHeight - feed.value.scrollTop : 0) })
</script>

<template>
  <section style="flex:1;min-width:0;background:var(--surface-panel);border-radius:var(--radius-panel);
                  display:flex;flex-direction:column;overflow:hidden">
    <PaneHeader
      :title="direct() ? displayName(title) : title"
      :subtitle="direct() ? '@' + title
                          : channel.member_count + (channel.member_count === 1 ? ' member' : ' members')"
      :subtitle-upper="!direct()"
      :open="infoOpen"
      @info="emit('info')"
    >
      <template #mark>
        <SgAvatar v-if="direct()" :initials="initials(displayName(title))" :src="avatarUrl(title)" :size="44" />
        <ChannelGlyph v-else :size="44" tone="blue" />
      </template>
    </PaneHeader>

    <div ref="feed" class="feed" @scroll="onScroll">
      <p v-if="!messages.length" class="sg-mono" style="margin:auto;color:var(--text-muted)">
        No messages yet
      </p>

      <MessageBubble
        v-for="({ m, head, quote }, i) in rows"
        :key="m.id || m.client_msg_id"
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
        :style="head && i > 0 ? { marginTop: direct() ? '10px' : '18px' } : null"
        @retry="emit('retry', m)"
        @discard="emit('discard', m)"
        :tabindex="m.id ? 0 : undefined"
        class="message"
        :class="{ lit: lit && lit === m.id }"
        :data-id="m.id"
        @contextmenu.prevent="openMenu(m, $event.clientX, $event.clientY)"
        @keydown.enter.self="openMenuAtBubble(m, $event)"
        @author="emit('person', m.author.username)"
        @quote="openQuote(m)"
        @picture="openPicture(m, $event)"
        @forwarded-author="m.forwarded.author.id === me.id || emit('person', m.forwarded.author.username)"
      >{{ m.text }}</MessageBubble>
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
    >
      <!-- a reply still needs its own text; a forward can go on its own -->
      <div v-if="pending" class="pending">
        <span class="bar" />
        <div style="flex:1;min-width:0;display:flex;flex-direction:column;gap:2px">
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
.feed {
  flex: 1;
  overflow-y: auto;
  padding: 22px 28px;
  display: flex;
  flex-direction: column;
  /* spacing inside a run; the first bubble of a run adds its own margin on top */
  gap: 4px;
}
/* short conversations hug the bottom; long ones still scroll from the top */
.feed > :first-child { margin-top: auto; }
.message { border-radius: var(--radius-bubble); }
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
</style>
