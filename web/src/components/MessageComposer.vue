<script setup>
import { computed, reactive, ref } from 'vue'
import { api } from '../api'
import { prepareForSending } from '../images'
import SendFilesDialog from './SendFilesDialog.vue'

const props = defineProps({
  placeholder: { type: String, default: 'Message…' },
  disabled: Boolean,
  maxLength: { type: Number, default: 4000 },
  // Something above the field (a forward) can be sent with no text of its own.
  ready: Boolean,
})
const emit = defineEmits(['send'])

const MAX_BYTES = 10 << 20
// At most this many pictures are prepared and uploaded at once, as Telegram's
// client limits its own uploads: the server holds a 16 MB driver buffer for each.
const MAX_PARALLEL = 3

const text = ref('')
const focused = ref(false)
const input = ref(null)
const picker = ref(null)

// Each picture uploads as soon as it is picked, so Send only has to name it. While
// there are any, they wait in the send box with a caption of their own.
// { key, url, name, size, status: 'queued' | 'uploading' | 'done' | 'failed', error, att }
const files = ref([])
// As in Telegram, what was typed moves into the caption when the box opens and
// comes back to the field when the box is closed without sending.
const caption = ref('')

const uploading = computed(() => files.value.some((f) => f.status === 'queued' || f.status === 'uploading'))
const broken = computed(() => files.value.some((f) => f.status === 'failed'))
const canSend = computed(() => !props.disabled && (!!text.value.trim() || props.ready))
const canSendFiles = computed(() =>
  !props.disabled && !uploading.value && !broken.value && files.value.length > 0)

function addFiles(list) {
  const opening = files.value.length === 0
  for (const file of list) {
    if (file.type && !file.type.startsWith('image/')) continue
    const item = reactive({
      key: crypto.randomUUID(),
      url: URL.createObjectURL(file),
      name: file.name || 'Pasted image',
      size: file.size,
      status: 'queued',
      error: '',
      att: null,
      dropped: false,
    })
    files.value.push(item)
    waiting.push({ item, file })
  }
  if (opening && files.value.length) {
    caption.value = text.value
    text.value = ''
  }
  pump()
}

function closeBox() {
  text.value = caption.value
  caption.value = ''
}

const waiting = []
let active = 0

// Starts queued pictures while there is room; each one that finishes makes room
// for the next. A picture removed while waiting is skipped.
function pump() {
  while (active < MAX_PARALLEL && waiting.length) {
    const { item, file } = waiting.shift()
    if (item.dropped) continue
    active++
    item.status = 'uploading'
    upload(item, file).finally(() => {
      active--
      pump()
    })
  }
}

// The limit is checked on what is sent, not on the original: a 12 MB photo is
// a few hundred KB once scaled down.
async function upload(item, file) {
  let blob
  try {
    blob = await prepareForSending(file)
  } catch {
    Object.assign(item, { status: 'failed', error: 'This browser cannot read the picture' })
    return
  }
  item.size = blob.size
  if (blob.size > MAX_BYTES) {
    Object.assign(item, { status: 'failed', error: 'Larger than 10 MB' })
    return
  }
  try {
    item.att = await api.uploadMedia(blob)
    item.status = 'done'
  } catch (e) {
    Object.assign(item, { status: 'failed', error: e.message })
  }
}

function pick(event) {
  addFiles(event.target.files)
  event.target.value = ''
}

function paste(event) {
  if (!event.clipboardData.files.length) return
  event.preventDefault()
  addFiles(event.clipboardData.files)
}

function remove(item) {
  item.dropped = true
  URL.revokeObjectURL(item.url)
  files.value = files.value.filter((f) => f !== item)
  if (!files.value.length) closeBox()
}

function clearFiles() {
  for (const f of files.value) {
    f.dropped = true
    URL.revokeObjectURL(f.url)
  }
  if (files.value.length) closeBox()
  files.value = []
}

// Picking Reply or Forward in a menu leaves focus nowhere; the chat puts it here.
defineExpose({ focus: () => input.value?.focus(), clearFiles })

// The local copies go along as previews for the bubble until the server answers,
// so their URLs are not revoked here.
function sendFiles() {
  if (!canSendFiles.value) return
  emit('send', caption.value, files.value.map((f) => ({ ...f.att, preview: f.url })))
  caption.value = ''
  files.value = []
  input.value?.focus()
}

function send() {
  if (!canSend.value) return
  emit('send', text.value, [])
  text.value = ''
}
</script>

<template>
  <div style="position:relative;padding:16px 28px 20px"
       @dragover.prevent @drop.prevent="addFiles($event.dataTransfer.files)">
    <slot />
    <SendFilesDialog
      v-if="files.length"
      v-model="caption"
      :files="files"
      :can-send="canSendFiles"
      :max-length="maxLength"
      @close="clearFiles(); input?.focus()"
      @remove="remove"
      @add="addFiles"
      @send="sendFiles"
    />
    <div style="display:flex;align-items:center;gap:16px">
      <div
        :style="{ flex: 1, display: 'flex', alignItems: 'center', gap: '12px', height: '48px',
                  padding: '0 24px 0 12px',
                  background: 'var(--surface-sunken)', borderRadius: 'var(--radius-pill)',
                  boxShadow: focused ? 'inset 0 0 0 2px var(--border-active)' : 'none' }"
      >
        <button type="button" class="attach" aria-label="Attach pictures" title="Attach pictures"
                :disabled="disabled" @click="picker.click()">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor"
               stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M21 11.5l-8.6 8.6a5.5 5.5 0 0 1-7.8-7.8l8.6-8.6a3.7 3.7 0 0 1 5.2 5.2l-8.6 8.6a1.8 1.8 0 0 1-2.6-2.6l7.9-7.9" />
          </svg>
        </button>
        <input ref="picker" type="file" accept="image/*" multiple hidden
               @change="pick">
        <input
          ref="input"
          v-model="text"
          :disabled="disabled"
          :placeholder="placeholder"
          :maxlength="maxLength"
          style="flex:1;min-width:0;border:none;outline:none;background:transparent;
                 font:var(--text-body);color:var(--text-primary)"
          @focus="focused = true"
          @blur="focused = false"
          @paste="paste"
          @keydown.enter.exact.prevent="send"
        >
      </div>
      <button
        type="button"
        :disabled="!canSend"
        :style="{ height: '48px', padding: '0 26px', border: 'none',
                  cursor: disabled || uploading ? 'not-allowed' : 'pointer', borderRadius: 'var(--radius-pill)',
                  background: disabled || uploading || broken ? 'var(--text-muted)' : 'var(--surface-accent)',
                  color: '#fff',
                  font: 'var(--text-machine-11)', textTransform: 'uppercase',
                  letterSpacing: 'var(--mono-tracking)' }"
        @click="send"
      >Send</button>
    </div>
  </div>
</template>

<style scoped>
.attach {
  display: flex;
  flex: none;
  padding: 6px;
  border: none;
  border-radius: var(--radius-pill);
  background: none;
  color: var(--text-muted);
  cursor: pointer;
}
.attach:hover:not(:disabled) { color: var(--blue); }
.attach:disabled { cursor: not-allowed; opacity: 0.5; }
</style>
