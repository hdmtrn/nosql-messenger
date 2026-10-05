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
  <header class="pane-header">
    <button
      type="button"
      class="toggle"
      :aria-expanded="open"
      @click="$emit('info')"
    >
      <slot name="mark" />
      <span class="titles">
        <span class="title-row">
          <span class="title">{{ title }}</span>
          <span aria-hidden="true" class="arrow" :class="{ open }">→</span>
        </span>
        <span
          v-if="subtitle"
          class="subtitle"
          :class="{ upper: subtitleUpper, accent: subtitleAccent }"
        >{{ subtitle }}</span>
      </span>
    </button>

    <div class="actions">
      <slot name="actions" />
    </div>
  </header>
</template>

<style scoped>
.pane-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  padding: 18px 28px;
  border-bottom: 1px solid var(--border-muted);
}
.toggle {
  display: flex;
  align-items: center;
  gap: 16px;
  background: none;
  border: none;
  padding: 0;
  cursor: pointer;
  text-align: left;
}
.titles {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.title-row {
  display: flex;
  align-items: center;
  gap: 12px;
}
.title {
  font: 600 24px/1.15 var(--font-ui);
  letter-spacing: -0.01em;
  color: var(--text-primary);
}
.arrow {
  display: inline-block;
  font: 400 18px/1 var(--font-ui);
  color: var(--grey);
  transition: transform 0.18s ease;
}
.arrow.open { transform: rotate(180deg); }
.subtitle {
  font: var(--text-machine-11);
  letter-spacing: var(--mono-tracking);
  color: var(--grey);
}
.subtitle.upper {
  font: var(--text-machine);
  text-transform: uppercase;
}
.subtitle.accent { color: var(--blue); }
.actions {
  display: flex;
  align-items: center;
  gap: 12px;
}
</style>
