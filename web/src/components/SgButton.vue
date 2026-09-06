<script setup>
import { computed } from 'vue'

const props = defineProps({
  variant: { type: String, default: 'primary' },
  size: { type: String, default: 'md' },
  disabled: Boolean,
  fullWidth: Boolean,
  mono: { type: Boolean, default: true },
  onBlue: Boolean,
  type: { type: String, default: 'button' },
})

const H = { sm: '26px', md: '36px', lg: '48px' }
const PAD = { sm: '0 12px', md: '0 18px', lg: '0 26px' }

const variants = computed(() => ({
  primary: { background: 'var(--surface-accent)', color: '#fff' },
  outline: {
    background: 'transparent',
    color: props.onBlue ? '#fff' : 'var(--text-primary)',
    boxShadow: `inset 0 0 0 1px ${props.onBlue ? 'rgba(255,255,255,0.7)' : 'var(--ink)'}`,
  },
  ghost: { background: 'transparent', color: props.onBlue ? '#fff' : 'var(--text-primary)' },
  danger: { background: 'var(--status-error)', color: '#fff' },
  mutedText: { background: 'transparent', color: 'var(--text-muted)', padding: 0, height: 'auto' },
}))

const style = computed(() => {
  const base = {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    gap: '8px',
    height: H[props.size],
    padding: PAD[props.size],
    borderRadius: 'var(--radius-pill)',
    cursor: props.disabled ? 'not-allowed' : 'pointer',
    font: props.mono
      ? props.size === 'sm' ? 'var(--text-machine)' : 'var(--text-machine-11)'
      : 'var(--text-ui)',
    textTransform: props.mono ? 'uppercase' : 'none',
    letterSpacing: props.mono ? 'var(--mono-tracking)' : '0',
    border: 'none',
    width: props.fullWidth ? '100%' : undefined,
    whiteSpace: 'nowrap',
  }
  const off = props.disabled
    ? {
        background: ['primary', 'danger'].includes(props.variant) ? 'var(--text-muted)' : 'transparent',
        color: ['primary', 'danger'].includes(props.variant) ? '#fff' : 'var(--text-disabled)',
        boxShadow: props.variant === 'outline' ? 'inset 0 0 0 1px var(--border-muted)' : 'none',
      }
    : null
  return { ...base, ...variants.value[props.variant], ...off }
})
</script>

<template>
  <button :type="type" :disabled="disabled" :style="style"><slot /></button>
</template>
