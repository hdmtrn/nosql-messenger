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
})
defineEmits(['retry', 'discard'])

const MONO = {
  font: 'var(--text-machine)',
  textTransform: 'uppercase',
  letterSpacing: 'var(--mono-tracking)',
}

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
const labelStyle = computed(() => ({
  ...MONO,
  color: props.status === 'failed' ? 'var(--status-error)' : 'var(--text-muted)',
}))
const avatarTone = computed(() =>
  props.own && props.status !== 'delivered' ? props.status : 'blue'
)
const bubbleStyle = computed(() => ({
  display: 'flex',
  alignItems: 'center',
  gap: '8px',
  maxWidth: 'min(560px, 100%)',
  padding: '11px 20px',
  borderRadius: props.own
    ? 'var(--radius-bubble) 0 var(--radius-bubble) var(--radius-bubble)'
    : '0 var(--radius-bubble) var(--radius-bubble) var(--radius-bubble)',
  font: 'var(--text-body)',
  ...shells.value[props.status],
}))
</script>

<template>
  <div :style="{ display: 'flex', gap: '12px', alignItems: 'flex-start',
                 flexDirection: own ? 'row-reverse' : 'row' }">
    <SgAvatar :initials="initials || (author || '').slice(0, 2)" :size="36" :tone="avatarTone" />

    <div :style="{ display: 'flex', flexDirection: 'column',
                   alignItems: own ? 'flex-end' : 'flex-start', gap: '6px', minWidth: 0 }">
      <div style="display:flex;gap:8px;align-items:baseline">
        <span v-if="author" style="font:600 13px/1.2 var(--font-ui)">{{ author }}</span>
        <span v-if="stateLabel" :style="labelStyle">{{ stateLabel }}</span>
        <span v-else-if="time" :style="{ ...MONO, color: 'var(--text-muted)' }">{{ time }}</span>
      </div>

      <div :style="bubbleStyle">
        <span><slot /></span>
        <SgSpinner v-if="status === 'sending'" />
      </div>

      <div v-if="status === 'failed'" style="display:flex;align-items:center;gap:12px">
        <SgButton variant="danger" size="sm" @click="$emit('retry')">Retry</SgButton>
        <SgButton variant="mutedText" @click="$emit('discard')">Discard</SgButton>
      </div>
    </div>
  </div>
</template>
