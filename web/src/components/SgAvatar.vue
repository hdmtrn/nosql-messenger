<script setup>
import { computed } from 'vue'

const props = defineProps({
  initials: { type: String, default: '' },
  size: { type: Number, default: 36 },
  tone: { type: String, default: 'blue' },
})

const tones = {
  blue: { background: 'var(--surface-accent)', color: '#fff' },
  sending: { background: 'var(--surface-panel)', color: 'var(--blue)', boxShadow: 'inset 0 0 0 2px var(--border-active)' },
  failed: { background: 'var(--surface-panel)', color: 'var(--status-error)', boxShadow: 'inset 0 0 0 2px var(--border-error)' },
  onBlue: { background: 'rgba(255,255,255,0.18)', color: '#fff' },
  ink: { background: 'var(--surface-inverse)', color: '#fff' },
}

const style = computed(() => ({
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  flex: 'none',
  width: props.size + 'px',
  height: props.size + 'px',
  borderRadius: 'var(--radius-pill)',
  font: props.size <= 24 ? 'var(--text-machine)' : 'var(--text-machine-11)',
  textTransform: 'uppercase',
  letterSpacing: 'var(--mono-tracking)',
  ...tones[props.tone],
}))
</script>

<template><span :style="style">{{ [...initials].slice(0, 2).join('') }}</span></template>
