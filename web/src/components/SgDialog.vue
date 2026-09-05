<script setup>
defineProps({ title: String })
defineEmits(['close'])
</script>

<template>
  <div
    style="position:fixed;inset:0;background:rgba(17,17,17,0.35);display:grid;
           place-items:center;z-index:10"
    @click.self="$emit('close')"
  >
    <div style="width:420px;background:var(--surface-panel);border-radius:var(--radius-panel);
                padding:var(--pad-panel);display:flex;flex-direction:column;gap:var(--gap-block)">
      <h2 style="margin:0;font:var(--text-title)">{{ title }}</h2>
      <slot />
    </div>
  </div>
</template>
