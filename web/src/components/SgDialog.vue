<script setup>
defineProps({ title: String })
defineEmits(['close'])
</script>

<template>
  <div class="sg-dialog" @click.self="$emit('close')">
    <div class="dialog">
      <h2 class="title">{{ title }}</h2>
      <slot />
    </div>
  </div>
</template>

<style scoped>
.sg-dialog {
  position: fixed;
  inset: 0;
  background: rgba(17, 17, 17, 0.35);
  display: grid;
  place-items: center;
  z-index: 10;
}
.dialog {
  width: 420px;
  background: var(--surface-panel);
  border-radius: var(--radius-panel);
  padding: var(--pad-panel);
  display: flex;
  flex-direction: column;
  gap: var(--gap-block);
}
.title {
  margin: 0;
  font: var(--text-title);
}
</style>
