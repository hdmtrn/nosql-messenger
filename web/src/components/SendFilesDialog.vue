<script setup>
import { computed, nextTick, onMounted, ref } from 'vue'
import SgSpinner from './SgSpinner.vue'

// The pictures picked for one message, laid out as Telegram's send box: a list to
// check and prune, one comment for all of them, Send. The files and the text
// belong to the field underneath; this box only shows and edits them.
const props = defineProps({
  // [{ key, url, name, size, status: 'uploading' | 'done' | 'failed', error }]
  files: { type: Array, required: true },
  canSend: Boolean,
  maxLength: { type: Number, default: 4000 },
})
const comment = defineModel({ type: String, default: '' })
const emit = defineEmits(['close', 'remove', 'add', 'send'])

const field = ref(null)
const picker = ref(null)
onMounted(() => nextTick(() => field.value?.focus()))

const title = computed(() => {
  const n = props.files.length
  return n === 1 ? '1 photo' : `${n} photos`
})

function size(bytes) {
  if (bytes >= 1 << 20) return `${(bytes / (1 << 20)).toFixed(1)} MB`
  return `${Math.max(1, Math.round(bytes / 1024))} KB`
}

function note(f) {
  if (f.status === 'uploading') return 'Uploading…'
  if (f.status === 'failed') return f.error || 'Not uploaded'
  return size(f.size)
}

function pick(event) {
  emit('add', event.target.files)
  event.target.value = ''
}

function paste(event) {
  if (!event.clipboardData.files.length) return
  event.preventDefault()
  emit('add', event.clipboardData.files)
}
</script>

<template>
  <Teleport to="body">
    <div class="overlay" @click.self="emit('close')" @keydown.esc="emit('close')"
         @dragover.prevent @drop.prevent="emit('add', $event.dataTransfer.files)">
      <div class="box" role="dialog" aria-modal="true" :aria-label="title">
        <header class="head">
          <button type="button" class="round" aria-label="Cancel" @click="emit('close')">×</button>
          <h2 class="title">{{ title }}</h2>
          <button type="button" class="round" aria-label="Add pictures" title="Add pictures"
                  @click="picker.click()">+</button>
          <input ref="picker" type="file" accept="image/png,image/jpeg,image/gif" multiple hidden
                 @change="pick">
        </header>

        <ul class="list">
          <li v-for="f in files" :key="f.key" class="row">
            <span class="thumb" :class="f.status">
              <img :src="f.url" alt="">
              <span v-if="f.status === 'uploading'" class="veil"><SgSpinner /></span>
            </span>
            <span class="meta">
              <span class="name">{{ f.name }}</span>
              <span class="note" :class="{ bad: f.status === 'failed' }">{{ note(f) }}</span>
            </span>
            <button type="button" class="remove" :aria-label="`Remove ${f.name}`"
                    @click="emit('remove', f)">×</button>
          </li>
        </ul>

        <footer class="foot">
          <input
            ref="field"
            v-model="comment"
            class="comment"
            placeholder="Add a comment…"
            :maxlength="maxLength"
            @paste="paste"
            @keydown.enter.exact.prevent="emit('send')"
          >
          <button type="button" class="send" :disabled="!canSend" @click="emit('send')">Send</button>
        </footer>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.overlay {
  position: fixed;
  inset: 0;
  z-index: 10;
  display: grid;
  place-items: center;
  padding: 32px 16px;
  background: rgba(17, 17, 17, 0.35);
}
.box {
  width: min(440px, 100%);
  max-height: 100%;
  display: flex;
  flex-direction: column;
  background: var(--surface-panel);
  border-radius: var(--radius-panel);
  overflow: hidden;
}
.head {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px 16px 8px;
}
.title {
  flex: 1;
  margin: 0;
  text-align: center;
  font: var(--text-title);
}
.round {
  flex: none;
  width: 40px;
  height: 40px;
  padding: 0;
  border: none;
  border-radius: var(--radius-pill);
  background: var(--surface-sunken);
  color: var(--text-primary);
  font: 300 24px/40px var(--font-ui);
  cursor: pointer;
}
.round:hover { color: var(--blue); }
.list {
  flex: 1;
  min-height: 0;
  margin: 0;
  padding: 8px 16px;
  list-style: none;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.row {
  display: flex;
  align-items: center;
  gap: 14px;
}
.thumb {
  position: relative;
  flex: none;
  width: 72px;
  height: 72px;
  border-radius: 12px;
  overflow: hidden;
  background: var(--surface-sunken);
}
.thumb img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.thumb.failed { box-shadow: inset 0 0 0 2px var(--border-error); }
.veil {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  background: rgba(255, 255, 255, 0.6);
}
.meta {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.name {
  font: 500 15px/1.3 var(--font-ui);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.note {
  font: var(--text-machine-11);
  letter-spacing: var(--mono-tracking);
  color: var(--text-muted);
}
.note.bad { color: var(--status-error); }
.remove {
  flex: none;
  width: 28px;
  height: 28px;
  padding: 0;
  border: none;
  border-radius: var(--radius-pill);
  background: none;
  color: var(--text-muted);
  font: 20px/28px var(--font-ui);
  cursor: pointer;
}
.remove:hover { color: var(--status-error); }
.foot {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px 16px;
}
.comment {
  flex: 1;
  min-width: 0;
  height: 48px;
  padding: 0 20px;
  border: none;
  outline: none;
  border-radius: var(--radius-pill);
  background: var(--surface-sunken);
  font: var(--text-body);
  color: var(--text-primary);
}
.comment:focus { box-shadow: inset 0 0 0 2px var(--border-active); }
.send {
  flex: none;
  height: 48px;
  padding: 0 26px;
  border: none;
  border-radius: var(--radius-pill);
  background: var(--surface-accent);
  color: #fff;
  font: var(--text-machine-11);
  text-transform: uppercase;
  letter-spacing: var(--mono-tracking);
  cursor: pointer;
}
.send:disabled {
  background: var(--text-muted);
  cursor: not-allowed;
}
</style>
