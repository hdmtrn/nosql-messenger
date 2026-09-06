<script setup>
import CodePill from './CodePill.vue'

defineProps({
  title: String,
  subtitle: String,
  // A handle reads as it is stored; a count is machine voice and shouts.
  subtitleUpper: { type: Boolean, default: true },
  code: String,
  copied: Boolean,
})
defineEmits(['copy', 'info'])
</script>

<template>
  <header style="display:flex;align-items:center;justify-content:space-between;gap:24px;
                 padding:18px 28px;border-bottom:1px solid var(--border-muted)">
    <button
      type="button"
      style="display:flex;align-items:center;gap:16px;background:none;border:none;
             padding:0;cursor:pointer;text-align:left"
      @click="$emit('info')"
    >
      <slot name="mark" />
      <span style="display:flex;flex-direction:column;gap:2px">
        <span style="display:flex;align-items:center;gap:12px">
          <span style="font:600 24px/1.15 var(--font-ui);letter-spacing:-0.01em;
                       color:var(--text-primary)">{{ title }}</span>
          <span aria-hidden="true" style="font:400 18px/1 var(--font-ui);color:var(--grey)">→</span>
        </span>
        <span
          v-if="subtitle"
          :style="{ font: subtitleUpper ? 'var(--text-machine)' : 'var(--text-machine-11)',
                    textTransform: subtitleUpper ? 'uppercase' : 'none',
                    letterSpacing: 'var(--mono-tracking)', color: 'var(--grey)' }"
        >{{ subtitle }}</span>
      </span>
    </button>

    <div style="display:flex;align-items:center;gap:12px">
      <slot name="actions" />
      <CodePill v-if="code" :code="code" :copied="copied" @copy="$emit('copy')" />
    </div>
  </header>
</template>
