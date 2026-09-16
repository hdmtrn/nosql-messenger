<script setup>
import { computed, ref, watch } from 'vue'

const props = defineProps({
  initials: { type: String, default: '' },
  // A picture that fails to load falls back to the initials.
  src: { type: String, default: '' },
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

const failed = ref(false)
watch(() => props.src, () => { failed.value = false })

const imageStyle = computed(() => ({
  display: 'block',
  flex: 'none',
  width: props.size + 'px',
  height: props.size + 'px',
  borderRadius: 'var(--radius-pill)',
  objectFit: 'cover',
  boxShadow: tones[props.tone]?.boxShadow,
}))
</script>

<template>
  <img v-if="src && !failed" :src="src" alt="" :style="imageStyle" @error="failed = true">
  <span v-else :style="style">{{ [...initials].slice(0, 2).join('') }}</span>
</template>
