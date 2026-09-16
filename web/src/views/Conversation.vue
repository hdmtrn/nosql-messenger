<script setup>
import { computed, nextTick, ref, watch } from 'vue'
import PaneHeader from '../components/PaneHeader.vue'
import MessageBubble from '../components/MessageBubble.vue'
import MessageComposer from '../components/MessageComposer.vue'
import MessageMenu from '../components/MessageMenu.vue'
import SgAvatar from '../components/SgAvatar.vue'
import ChannelGlyph from '../components/ChannelGlyph.vue'
import { displayName, initials } from '../naming'

const props = defineProps({
  me: { type: Object, required: true },
  channel: { type: Object, required: true },
  title: { type: String, required: true },
  messages: { type: Array, default: () => [] },
  // username whose page is open beside the feed
  person: { type: String, default: '' },
  forwardTargets: { type: Array, default: () => [] },
  // The message waiting above the field to be forwarded into this chat on Send.
  pendingForward: { type: Object, default: null },
})

const emit = defineEmits(['send', 'retry', 'discard', 'load-older', 'info', 'person',
  'forward', 'cancel-forward'])

const feed = ref(null)

// The message whose menu is open, and where the menu stands.
const menu = ref(null)

// Only a stored message has an id the server can forward; one still sending or
// failed has nothing to act on yet.
function openMenu(m, x, y) {
  if (m.id) menu.value = { m, x, y }
}

// A forward of a forward names the original author, as the server will store it.
const forwardAuthor = computed(() => {
  const m = props.pendingForward
  return m ? displayName((m.forwarded || m).author.username) : ''
})
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
const rows = computed(() =>
  props.messages.map((m, i) => {
    const prev = props.messages[i - 1]
    const head =
      !prev ||
      prev.author.id !== m.author.id ||
      new Date(m.created_at) - new Date(prev.created_at) > RUN_BREAK_MS
    return { m, head }
  })
)

function clock(iso) {
  return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
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

defineExpose({ toBottom, keepPosition, distanceFromBottom: () => (feed.value ? feed.value.scrollHeight - feed.value.scrollTop : 0) })
</script>

<template>
  <section style="flex:1;min-width:0;background:var(--surface-panel);border-radius:var(--radius-panel);
                  display:flex;flex-direction:column;overflow:hidden">
    <PaneHeader
      :title="direct() ? displayName(title) : title"
      :subtitle="direct() ? '@' + title
                          : channel.member_count + (channel.member_count === 1 ? ' member' : ' members')"
      :subtitle-upper="!direct()"
      @info="emit('info')"
    >
      <template #mark>
        <SgAvatar v-if="direct()" :initials="initials(displayName(title))" :size="44" />
        <ChannelGlyph v-else :size="44" tone="blue" />
      </template>
    </PaneHeader>

    <div ref="feed" class="feed" @scroll="onScroll">
      <p v-if="!messages.length" class="sg-mono" style="margin:auto;color:var(--text-muted)">
        No messages yet
      </p>

      <MessageBubble
        v-for="({ m, head }, i) in rows"
        :key="m.id || m.client_msg_id"
        :own="m.author.id === me.id"
        :head="head"
        :status="m.status || 'delivered'"
        :author="direct() ? '' : displayName(m.author.username)"
        :initials="initials(displayName(m.author.username))"
        :time="m.status && m.status !== 'delivered' ? '' : clock(m.created_at)"
        :ring="!!person && m.author.username === person"
        :forwarded="m.forwarded ? displayName(m.forwarded.author.username) : ''"
        :style="head && i > 0 ? { marginTop: direct() ? '10px' : '18px' } : null"
        @retry="emit('retry', m)"
        @discard="emit('discard', m)"
        :tabindex="m.id ? 0 : undefined"
        class="message"
        @contextmenu.prevent="openMenu(m, $event.clientX, $event.clientY)"
        @keydown.enter.self="openMenuAtBubble(m, $event)"
        @author="emit('person', m.author.username)"
      >{{ m.text }}</MessageBubble>
    </div>

    <MessageMenu
      v-if="menu"
      :x="menu.x"
      :y="menu.y"
      :targets="forwardTargets"
      @copy="copyText"
      @forward="forward"
      @close="menu = null"
    />

    <MessageComposer
      :placeholder="pendingForward ? 'Add a comment…'
                    : direct() ? `Message ${displayName(title)}…` : `Message #${channel.name}…`"
      :ready="!!pendingForward"
      @send="emit('send', $event)"
    >
      <div v-if="pendingForward" class="pending">
        <span class="bar" />
        <div style="flex:1;min-width:0;display:flex;flex-direction:column;gap:2px">
          <span class="pending-label">Forward from {{ forwardAuthor }}</span>
          <span class="pending-text">{{ pendingForward.text }}</span>
        </div>
        <button type="button" class="cancel" aria-label="Cancel forward"
                @click="emit('cancel-forward')">×</button>
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
