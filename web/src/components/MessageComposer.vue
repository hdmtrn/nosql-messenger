<script setup>
import { computed, reactive, ref } from 'vue'
import { api } from '../api'
import SgSpinner from './SgSpinner.vue'

const props = defineProps({
  placeholder: { type: String, default: 'Message…' },
  disabled: Boolean,
  maxLength: { type: Number, default: 4000 },
  // Something above the field (a forward) can be sent with no text of its own.
  ready: Boolean,
})
const emit = defineEmits(['send'])

const MAX_BYTES = 10 << 20

const text = ref('')
const focused = ref(false)
const input = ref(null)
const picker = ref(null)

// Each picture uploads as soon as it is picked, so Send only has to name it.
// { key, url, status: 'uploading' | 'done' | 'failed', error, att }
const files = ref([])

const uploading = computed(() => files.value.some((f) => f.status === 'uploading'))
const broken = computed(() => files.value.some((f) => f.status === 'failed'))
const canSend = computed(() =>
  !props.disabled && !uploading.value && !broken.value
  && (!!text.value.trim() || props.ready || files.value.length > 0))

function addFiles(list) {
  for (const file of list) {
    if (file.type && !file.type.startsWith('image/')) continue
    const item = reactive({
      key: crypto.randomUUID(),
      url: URL.createObjectURL(file),
      status: 'uploading',
      error: '',
      att: null,
    })
    files.value.push(item)
    if (file.size > MAX_BYTES) {
      Object.assign(item, { status: 'failed', error: 'Larger than 10 MB' })
      continue
    }
    api.uploadMedia(file)
      .then((att) => Object.assign(item, { status: 'done', att }))
      .catch((e) => Object.assign(item, { status: 'failed', error: e.message }))
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
  URL.revokeObjectURL(item.url)
  files.value = files.value.filter((f) => f !== item)
}

function clearFiles() {
  for (const f of files.value) URL.revokeObjectURL(f.url)
  files.value = []
}

// Picking Reply or Forward in a menu leaves focus nowhere; the chat puts it here.
defineExpose({ focus: () => input.value?.focus(), clearFiles })

// The local copies go along as previews for the bubble until the server answers,
// so their URLs are not revoked here.
function send() {
  if (!canSend.value) return
  emit('send', text.value, files.value.map((f) => ({ ...f.att, preview: f.url })))
  text.value = ''
  files.value = []
}
</script>

<template>
  <div style="position:relative;padding:16px 28px 20px"
       @dragover.prevent @drop.prevent="addFiles($event.dataTransfer.files)">
    <slot />
    <div v-if="files.length" class="files">
      <div v-for="f in files" :key="f.key" class="file" :class="f.status" :title="f.error || undefined">
        <img :src="f.url" alt="">
        <span v-if="f.status === 'uploading'" class="veil"><SgSpinner /></span>
        <span v-else-if="f.status === 'failed'" class="veil">!</span>
        <button type="button" class="drop" aria-label="Remove picture" @click="remove(f)">×</button>
      </div>
    </div>
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
        <input ref="picker" type="file" accept="image/png,image/jpeg,image/gif" multiple hidden
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
.files {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 12px;
}
.file {
  position: relative;
  width: 64px;
  height: 64px;
  border-radius: 12px;
  overflow: hidden;
  background: var(--surface-sunken);
}
.file img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.file.failed { box-shadow: inset 0 0 0 2px var(--border-error); }
.veil {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(255, 255, 255, 0.6);
  font: 600 20px/1 var(--font-ui);
  color: var(--status-error);
}
.drop {
  position: absolute;
  top: 2px;
  right: 2px;
  width: 20px;
  height: 20px;
  padding: 0;
  border: none;
  border-radius: var(--radius-pill);
  background: rgba(0, 0, 0, 0.55);
  color: #fff;
  font: 14px/20px var(--font-ui);
  cursor: pointer;
}
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
