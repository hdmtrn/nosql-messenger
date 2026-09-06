<script setup>
import { nextTick, ref, watch } from 'vue'
import PaneHeader from '../components/PaneHeader.vue'
import MessageBubble from '../components/MessageBubble.vue'
import MessageComposer from '../components/MessageComposer.vue'
import SgAvatar from '../components/SgAvatar.vue'
import ChannelGlyph from '../components/ChannelGlyph.vue'
import { initials } from '../naming'

const props = defineProps({
  me: { type: Object, required: true },
  channel: { type: Object, required: true },
  title: { type: String, required: true },
  messages: { type: Array, default: () => [] },
})

const emit = defineEmits(['send', 'retry', 'discard', 'load-older', 'info'])

const feed = ref(null)
const copied = ref(false)

const direct = () => props.channel.kind === 'direct'

function clock(iso) {
  return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function copyCode() {
  navigator.clipboard.writeText(props.channel.invite_code)
  copied.value = true
  setTimeout(() => (copied.value = false), 1500)
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

watch(() => props.channel.id, () => {
  copied.value = false
  toBottom()
})

defineExpose({ toBottom, keepPosition, distanceFromBottom: () => (feed.value ? feed.value.scrollHeight - feed.value.scrollTop : 0) })
</script>

<template>
  <section style="flex:1;min-width:0;background:var(--surface-panel);border-radius:var(--radius-panel);
                  display:flex;flex-direction:column;overflow:hidden">
    <PaneHeader
      :title="title"
      :subtitle="direct() ? '@' + title : channel.member_count + ' members'"
      :subtitle-upper="!direct()"
      :code="direct() ? '' : (channel.invite_code || '').slice(0, 10) + '…'"
      :copied="copied"
      @copy="copyCode"
      @info="emit('info')"
    >
      <template #mark>
        <SgAvatar v-if="direct()" :initials="initials(title)" :size="44" />
        <ChannelGlyph v-else :size="44" tone="blue" />
      </template>
    </PaneHeader>

    <div ref="feed" class="feed" :style="{ gap: direct() ? '10px' : '18px' }" @scroll="onScroll">
      <p v-if="!messages.length" class="sg-mono" style="margin:auto;color:var(--text-muted)">
        No messages yet
      </p>

      <MessageBubble
        v-for="m in messages"
        :key="m.id || m.client_msg_id"
        :own="m.author.id === me.id"
        :status="m.status || 'delivered'"
        :author="direct() ? '' : m.author.username"
        :initials="initials(m.author.username)"
        :time="m.status && m.status !== 'delivered' ? '' : clock(m.created_at)"
        @retry="emit('retry', m)"
        @discard="emit('discard', m)"
      >{{ m.text }}</MessageBubble>
    </div>

    <MessageComposer
      :placeholder="direct() ? `Message ${title}…` : `Message #${channel.name}…`"
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
}
/* short conversations hug the bottom; long ones still scroll from the top */
.feed > :first-child { margin-top: auto; }
</style>
