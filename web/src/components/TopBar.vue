<script setup>
import SgAvatar from './SgAvatar.vue'
import SgButton from './SgButton.vue'

defineProps({
  product: { type: String, default: 'Messenger' },
  user: String,
  initials: String,
})
defineEmits(['log-out'])

const bar = { width: '14px', height: '1.5px', background: 'var(--ink)' }
</script>

<template>
  <header style="display:flex;align-items:center;justify-content:space-between;height:56px;padding:0 8px">
    <div style="display:flex;align-items:center;gap:16px">
      <span
        aria-hidden="true"
        style="display:inline-flex;align-items:center;justify-content:center;width:34px;height:34px;
               border-radius:var(--radius-pill);background:transparent;border:1.5px solid var(--ink);
               padding:0;gap:3px;flex-direction:column"
      >
        <span :style="bar" /><span :style="bar" /><span :style="bar" />
      </span>
      <span style="font:700 22px/1.15 var(--font-ui);letter-spacing:-0.01em">{{ product }}</span>
    </div>

    <div style="display:flex;align-items:center;gap:12px">
      <SgAvatar :initials="initials" :size="28" />
      <span style="font:var(--text-ui)">{{ user }}</span>
      <SgButton variant="primary" size="sm" @click="$emit('log-out')">Log out</SgButton>
    </div>
  </header>
</template>
