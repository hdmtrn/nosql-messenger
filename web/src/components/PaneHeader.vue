<script setup>
import BackButton from './BackButton.vue'

defineProps({
  title: String,
  subtitle: String,
  // Something happening now (typing) reads in the accent colour, as in Telegram.
  subtitleAccent: Boolean,
  // The info panel is open: the arrow turns back towards the feed, like the rail's ‹ ›.
  open: Boolean,
  // A phone leads with the way back to the list, and the unread waiting there.
  back: Boolean,
  backCount: { type: Number, default: 0 },
})
defineEmits(['info', 'back'])
</script>

<template>
  <header class="pane-header">
    <BackButton v-if="back" :count="backCount" @click="$emit('back')" />
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
          :class="{ accent: subtitleAccent }"
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
  min-width: 0;
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
  min-width: 0;
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
  font: var(--text-meta);
  color: var(--grey);
}
.subtitle.accent { color: var(--blue); }
.actions {
  display: flex;
  align-items: center;
  gap: 12px;
}
/* Telegram's mobile header: 56px tall, a 40px avatar, the title at 17px. */
@media (max-width: 600px), (max-width: 950px) and (max-height: 450px) {
  .pane-header {
    justify-content: flex-start;
    gap: 4px;
    min-height: 56px;
    padding: max(4px, env(safe-area-inset-top)) max(8px, env(safe-area-inset-right)) 4px max(4px, env(safe-area-inset-left));
  }
  .toggle {
    flex: 1;
    gap: 12px;
    min-height: 48px;
  }
  .title {
    font: var(--weight-semibold) 17px/1.25 var(--font-ui);
    letter-spacing: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .title-row { min-width: 0; }
  .arrow { display: none; }
  .subtitle {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}
</style>
