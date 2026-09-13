<script setup>
import { computed, nextTick, ref, watch } from 'vue'
import PaneHeader from '../components/PaneHeader.vue'
import MessageBubble from '../components/MessageBubble.vue'
import MessageComposer from '../components/MessageComposer.vue'
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
})

const emit = defineEmits(['send', 'retry', 'discard', 'load-older', 'info', 'person'])

const feed = ref(null)

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
        :style="head && i > 0 ? { marginTop: direct() ? '10px' : '18px' } : null"
        @retry="emit('retry', m)"
        @discard="emit('discard', m)"
        @author="emit('person', m.author.username)"
      >{{ m.text }}</MessageBubble>
    </div>

    <MessageComposer
      :placeholder="direct() ? `Message ${displayName(title)}…` : `Message #${channel.name}…`"
      @send="emit('send', $event)"
    />
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
</style>
