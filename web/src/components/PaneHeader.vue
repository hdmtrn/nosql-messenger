<script setup>
defineProps({
  title: String,
  subtitle: String,
  // A handle reads as it is stored; a count is machine voice and shouts.
  subtitleUpper: { type: Boolean, default: true },
  // Something happening now (typing) reads in the accent colour, as in Telegram.
  subtitleAccent: Boolean,
  // The info panel is open: the arrow turns back towards the feed, like the rail's ‹ ›.
  open: Boolean,
})
defineEmits(['info'])
</script>

<template>
  <header style="display:flex;align-items:center;justify-content:space-between;gap:24px;
                 padding:18px 28px;border-bottom:1px solid var(--border-muted)">
    <button
      type="button"
      style="display:flex;align-items:center;gap:16px;background:none;border:none;
             padding:0;cursor:pointer;text-align:left"
      :aria-expanded="open"
      @click="$emit('info')"
    >
      <slot name="mark" />
      <span style="display:flex;flex-direction:column;gap:2px">
        <span style="display:flex;align-items:center;gap:12px">
          <span style="font:600 24px/1.15 var(--font-ui);letter-spacing:-0.01em;
                       color:var(--text-primary)">{{ title }}</span>
          <span aria-hidden="true"
                style="display:inline-block;font:400 18px/1 var(--font-ui);color:var(--grey);
                       transition:transform 0.18s ease"
                :style="{ transform: open ? 'rotate(180deg)' : 'none' }">→</span>
        </span>
        <span
          v-if="subtitle"
          :style="{ font: subtitleUpper ? 'var(--text-machine)' : 'var(--text-machine-11)',
                    textTransform: subtitleUpper ? 'uppercase' : 'none',
                    letterSpacing: 'var(--mono-tracking)',
                    color: subtitleAccent ? 'var(--blue)' : 'var(--grey)' }"
        >{{ subtitle }}</span>
      </span>
    </button>

    <div style="display:flex;align-items:center;gap:12px">
      <slot name="actions" />
    </div>
  </header>
</template>
