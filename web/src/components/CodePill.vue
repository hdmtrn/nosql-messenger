<script setup>
defineProps({
  code: String,
  label: { type: String, default: 'Code' },
  copied: Boolean,
})
defineEmits(['copy'])

const mono = {
  font: 'var(--text-machine)',
  textTransform: 'uppercase',
  letterSpacing: 'var(--mono-tracking)',
}
</script>

<template>
  <span style="display:inline-flex;align-items:center;gap:10px;height:30px;
               padding:0 4px 0 14px;border-radius:var(--radius-pill);
               background:var(--surface-panel);box-shadow:inset 0 0 0 1px var(--border-muted)">
    <span :style="{ ...mono, color: 'var(--text-muted)' }">{{ label }}</span>
    <span style="font:var(--text-machine-11);letter-spacing:var(--mono-tracking);color:var(--text-primary)">
      {{ code }}
    </span>
    <button
      type="button"
      :style="{ ...mono, height: '22px', padding: '0 10px', border: 'none', cursor: 'pointer',
                borderRadius: 'var(--radius-pill)', background: 'var(--surface-accent)', color: '#fff' }"
      @click="$emit('copy')"
    >{{ copied ? 'Copied' : 'Copy' }}</button>
  </span>
</template>
