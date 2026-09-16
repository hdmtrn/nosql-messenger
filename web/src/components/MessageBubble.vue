<script setup>
import { computed } from 'vue'
import SgAvatar from './SgAvatar.vue'
import SgButton from './SgButton.vue'
import SgSpinner from './SgSpinner.vue'

const props = defineProps({
  own: Boolean,
  status: { type: String, default: 'delivered' },
  author: String,
  initials: String,
  time: String,
  // First message of a run by the same author: it carries the avatar and the
  // header. The rest of the run is bare bubbles under it.
  head: { type: Boolean, default: true },
  // The author whose page is open beside the feed gets a ring, so it stays clear
  // whose page that is.
  ring: Boolean,
  // Display name of the original author when this message is a forward.
  forwarded: { type: String, default: '' },
})
defineEmits(['retry', 'discard', 'author'])

const MONO = {
  font: 'var(--text-machine)',
  textTransform: 'uppercase',
  letterSpacing: 'var(--mono-tracking)',
}

const AVATAR = 36

const shells = computed(() => ({
  delivered: props.own
    ? { background: 'var(--surface-accent)', color: '#fff' }
    : { background: 'var(--surface-sunken)', color: 'var(--text-primary)' },
  sending: { background: 'var(--surface-panel)', color: 'var(--text-primary)', boxShadow: 'inset 0 0 0 2px var(--border-active)' },
  failed: { background: 'var(--surface-panel)', color: 'var(--text-primary)', boxShadow: 'inset 0 0 0 2px var(--border-error)' },
}))

const stateLabel = computed(() =>
  props.status === 'sending' ? 'Sending' : props.status === 'failed' ? 'Not sent' : null
)
// The header is now down to the author's name, so it exists only on the first
// message of a run in a group channel — or to explain a delivery problem.
const showHeader = computed(() => (props.head && !!props.author) || stateLabel.value !== null)
const labelStyle = computed(() => ({
  ...MONO,
  color: props.status === 'failed' ? 'var(--status-error)' : 'var(--text-muted)',
}))
// Low contrast on purpose: the clock rides along with every message, so it has
// to stay readable without competing with the text next to it.
const timeStyle = computed(() => ({
  ...MONO,
  flex: '0 0 auto',
  color: props.own ? 'rgba(255, 255, 255, 0.7)' : 'var(--text-muted)',
}))
const forwardStyle = computed(() => ({
  font: '500 13px/1.3 var(--font-ui)',
  color: props.own && props.status === 'delivered' ? 'rgba(255, 255, 255, 0.7)' : 'var(--text-muted)',
}))
const avatarTone = computed(() =>
  props.own && props.status !== 'delivered' ? props.status : 'blue'
)
// The flat corner is the tail pointing at the avatar, so only the head has one.
const corners = computed(() => {
  const r = 'var(--radius-bubble)'
  if (!props.head) return r
  return props.own ? `${r} 0 ${r} ${r}` : `0 ${r} ${r} ${r}`
})
const bubbleStyle = computed(() => ({
  display: 'flex',
  // the clock hangs off the last line of the text, Telegram-style
  alignItems: 'flex-end',
  gap: '8px',
  maxWidth: 'min(560px, 100%)',
  padding: '11px 20px',
  borderRadius: corners.value,
  font: 'var(--text-body)',
  ...shells.value[props.status],
}))
</script>

<template>
  <div :style="{ display: 'flex', gap: '12px', alignItems: 'flex-start',
                 flexDirection: own ? 'row-reverse' : 'row' }">
    <!-- someone else's avatar and name open their page; your own lead nowhere -->
    <component :is="own ? 'span' : 'button'" v-if="head" :type="own ? undefined : 'button'"
               class="who" :class="{ ring }" :aria-label="own ? undefined : 'Open profile'"
               @click="own || $emit('author')">
      <SgAvatar :initials="initials" :size="AVATAR" :tone="avatarTone" />
    </component>
    <!-- keeps the bubbles of a run flush with the head above them -->
    <div v-else :style="{ flex: `0 0 ${AVATAR}px` }" />

    <div :style="{ display: 'flex', flexDirection: 'column',
                   alignItems: own ? 'flex-end' : 'flex-start', gap: '6px', minWidth: 0 }">
      <div v-if="showHeader" style="display:flex;gap:8px;align-items:baseline">
        <component :is="own ? 'span' : 'button'" v-if="head && author" :type="own ? undefined : 'button'"
                   class="who name" @click="own || $emit('author')">{{ author }}</component>
        <span v-if="stateLabel" :style="labelStyle">{{ stateLabel }}</span>
      </div>

      <div :style="bubbleStyle">
        <!-- the source sits above the text, so the clock still hangs off its last line -->
        <span style="display:flex;flex-direction:column;gap:4px;min-width:0">
          <span v-if="forwarded" :style="forwardStyle">Forwarded from {{ forwarded }}</span>
          <span><slot /></span>
        </span>
        <SgSpinner v-if="status === 'sending'" />
        <span v-else-if="time" :style="timeStyle">{{ time }}</span>
      </div>

      <div v-if="status === 'failed'" style="display:flex;align-items:center;gap:12px">
        <SgButton variant="danger" size="sm" @click="$emit('retry')">Retry</SgButton>
        <SgButton variant="mutedText" @click="$emit('discard')">Discard</SgButton>
      </div>
    </div>
  </div>
</template>

<style scoped>
.who {
  display: flex;
  flex: none;
  padding: 0;
  border: none;
  border-radius: var(--radius-pill);
  background: none;
  color: inherit;
}
button.who { cursor: pointer; }
.name { font: 600 13px/1.2 var(--font-ui); }
button.name:hover { text-decoration: underline; }
/* the paper-coloured gap keeps the ring from merging into a blue avatar */
.ring { box-shadow: 0 0 0 2px var(--surface-panel), 0 0 0 4px var(--blue); }
</style>
