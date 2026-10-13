<script setup>
import { computed, ref } from 'vue'
import SgButton from './SgButton.vue'
import SgDialog from './SgDialog.vue'

defineProps({ name: { type: String, required: true }, error: String })
const emit = defineEmits(['send', 'close'])

// The server's limit, counted the way it counts: by characters, after trimming.
const limit = 200
const note = ref('')
const length = computed(() => [...note.value.trim()].length)
</script>

<template>
  <!-- Opened from the side panels too: a size container would pin a fixed dialog to it. -->
  <Teleport to="body">
    <SgDialog :title="`Add ${name} to friends`" @close="emit('close')">
      <label class="field">
        <span class="label">Message (optional)</span>
        <textarea v-model="note" class="note" rows="3" placeholder="Say who you are or where you met" />
        <span class="meta" :class="{ over: length > limit }">{{ error || `${length}/${limit}` }}</span>
      </label>
      <div class="dialog-actions">
        <SgButton variant="primary" :disabled="length > limit" @click="emit('send', note.trim())">Send request</SgButton>
        <SgButton variant="outline" @click="emit('close')">Cancel</SgButton>
      </div>
    </SgDialog>
  </Teleport>
</template>

<style scoped>
.field {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.label {
  font: var(--text-label);
  color: var(--text-primary);
}
.note {
  resize: none;
  padding: 12px 20px;
  border: none;
  outline: none;
  border-radius: var(--radius-panel);
  background: var(--surface-sunken);
  color: var(--text-primary);
  font: var(--text-body);
}
.note:focus { box-shadow: inset 0 0 0 2px var(--border-active); }
.meta {
  align-self: flex-end;
  font: var(--text-meta);
  color: var(--text-muted);
}
.over { color: var(--status-error); }
.dialog-actions {
  display: flex;
  gap: 12px;
}
</style>
