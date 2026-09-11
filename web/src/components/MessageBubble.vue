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
})
defineEmits(['retry', 'discard'])

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
    <SgAvatar v-if="head" :initials="initials || (author || '').slice(0, 2)" :size="AVATAR" :tone="avatarTone" />
    <!-- keeps the bubbles of a run flush with the head above them -->
    <div v-else :style="{ flex: `0 0 ${AVATAR}px` }" />

    <div :style="{ display: 'flex', flexDirection: 'column',
                   alignItems: own ? 'flex-end' : 'flex-start', gap: '6px', minWidth: 0 }">
      <div v-if="showHeader" style="display:flex;gap:8px;align-items:baseline">
        <span v-if="head && author" style="font:600 13px/1.2 var(--font-ui)">{{ author }}</span>
        <span v-if="stateLabel" :style="labelStyle">{{ stateLabel }}</span>
      </div>

      <div :style="bubbleStyle">
        <span><slot /></span>
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
