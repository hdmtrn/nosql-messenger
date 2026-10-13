<script setup>
defineProps({
  // Unread messages in the other chats, shown on the arrow as Telegram does.
  count: { type: Number, default: 0 },
})
defineEmits(['click'])
</script>

<template>
  <button type="button" class="back-button" aria-label="Back" @click="$emit('click')">
    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor"
         stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M20 12H4M10 6l-6 6 6 6" />
    </svg>
    <span v-if="count > 0" class="count">{{ count > 99 ? '99+' : count }}</span>
  </button>
</template>

<style scoped>
/* 48px: the touch target Material asks for, which also covers Apple's 44pt. */
.back-button {
  position: relative;
  flex: none;
  display: grid;
  place-items: center;
  width: 48px;
  height: 48px;
  padding: 0;
  border: none;
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--text-primary);
  cursor: pointer;
}
.count {
  position: absolute;
  top: 4px;
  right: 0;
  min-width: 20px;
  padding: 0 6px;
  border-radius: var(--radius-pill);
  background: var(--text-muted);
  color: #fff;
  font: var(--weight-medium) var(--size-12)/20px var(--font-ui);
  text-align: center;
}
</style>
