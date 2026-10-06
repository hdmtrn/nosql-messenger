<script setup>
import SgAvatar from '../components/SgAvatar.vue'
import SgButton from '../components/SgButton.vue'
import { avatarUrl, displayName, initials } from '../naming'

// Both lists belong to the messenger, which keeps them current on every
// friends event; the panel only shows them and passes the answers up.
defineProps({
  incoming: { type: Array, default: () => [] }, // requests, as the server sends them
  sent: { type: Array, default: () => [] }, // usernames
})
const emit = defineEmits(['close', 'respond', 'person'])
</script>

<template>
  <aside class="requests-panel">
    <div class="top">
      <h2 class="heading">Friend requests</h2>
      <SgButton variant="outline" size="sm" @click="emit('close')">Close</SgButton>
    </div>

    <span class="label section">Incoming</span>
    <div class="card list">
      <p v-if="!incoming.length" class="row none">None</p>
      <div v-for="r in incoming" :key="r.id" class="row">
        <button type="button" class="who" @click="emit('person', r.from.username)">
          <SgAvatar :initials="initials(displayName(r.from.username))" :src="avatarUrl(r.from.username)" :size="32" />
          <span class="who-name">{{ displayName(r.from.username) }}</span>
        </button>
        <SgButton variant="primary" size="sm" @click="emit('respond', r.id, 'accept')">Accept</SgButton>
        <SgButton variant="outline" size="sm" @click="emit('respond', r.id, 'decline')">Decline</SgButton>
      </div>
    </div>

    <span class="label section">Sent</span>
    <div class="card list">
      <p v-if="!sent.length" class="row none">None</p>
      <div v-for="u in sent" :key="u" class="row">
        <button type="button" class="who" @click="emit('person', u)">
          <SgAvatar :initials="initials(displayName(u))" :src="avatarUrl(u)" :size="32" />
          <span class="who-name">{{ displayName(u) }}</span>
        </button>
      </div>
    </div>
  </aside>
</template>

<style scoped>
.requests-panel {
  flex: 0 0 340px;
  display: flex;
  flex-direction: column;
  gap: 16px;
  overflow-y: auto;
}
.top {
  display: flex;
  align-items: center;
  gap: 12px;
}
.heading {
  margin: 0;
  flex: 1;
  font: 600 20px/1.2 var(--font-ui);
}
.card {
  background: var(--surface-panel);
  border-radius: var(--radius-panel);
}
.label {
  font: var(--text-label);
  color: var(--grey);
}
.section { padding: 0 24px; }
.list { padding: 4px 0; }
.row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 16px 0 24px;
  height: 52px;
}
.none {
  margin: 0;
  font: var(--text-body);
  color: var(--text-muted);
}
/* Avatar and name together open that person's page. */
.who {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 0;
  border: none;
  background: none;
  cursor: pointer;
}
.who-name {
  flex: 1;
  min-width: 0;
  text-align: left;
  font: var(--text-body);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
