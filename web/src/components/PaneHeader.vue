<script setup>
import CodePill from './CodePill.vue'

defineProps({ title: String, code: String, copied: Boolean })
defineEmits(['copy'])
</script>

<template>
  <header style="display:flex;align-items:center;justify-content:space-between;gap:24px;
                 padding:22px 28px 18px;border-bottom:1px solid var(--border-muted)">
    <h1 style="margin:0;font:600 24px/1.15 var(--font-ui);letter-spacing:-0.01em">{{ title }}</h1>
    <div style="display:flex;align-items:center;gap:12px">
      <slot name="actions" />
      <CodePill v-if="code" :code="code" :copied="copied" @copy="$emit('copy')" />
    </div>
  </header>
</template>
