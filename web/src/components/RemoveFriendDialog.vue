<script setup>
import SgButton from './SgButton.vue'
import SgDialog from './SgDialog.vue'

defineProps({ name: { type: String, required: true } })
const emit = defineEmits(['remove', 'close'])
</script>

<template>
  <!-- Opened from the side panels, inside the stage: a size container would pin a fixed dialog to it. -->
  <Teleport to="body">
    <SgDialog :title="`Remove ${name} from friends?`" @close="emit('close')">
      <p class="dialog-text">
        Your conversation stays where it is, and either of you can send a friend request again.
      </p>
      <div class="dialog-actions">
        <SgButton variant="danger" @click="emit('remove')">Remove</SgButton>
        <SgButton variant="outline" @click="emit('close')">Cancel</SgButton>
      </div>
    </SgDialog>
  </Teleport>
</template>

<style scoped>
.dialog-text {
  margin: 0;
  font: var(--text-body);
  color: var(--text-muted);
}
.dialog-actions {
  display: flex;
  gap: 12px;
}
</style>
