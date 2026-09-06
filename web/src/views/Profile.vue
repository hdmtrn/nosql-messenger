<script setup>
import { computed, ref } from 'vue'
import { api } from '../api'
import SgAvatar from '../components/SgAvatar.vue'
import SgButton from '../components/SgButton.vue'

const props = defineProps({ me: { type: Object, required: true } })
const emit = defineEmits(['saved', 'log-out'])

const displayName = ref(props.me.display_name)
const bio = ref(props.me.bio || '')
const error = ref('')

const sessions = ref([])
const sessionsOpen = ref(false)

const mono = {
  font: 'var(--text-machine)',
  textTransform: 'uppercase',
  letterSpacing: 'var(--mono-tracking)',
  color: 'var(--grey)',
}
const handle = {
  font: 'var(--text-machine-11)',
  letterSpacing: 'var(--mono-tracking)',
  color: 'var(--grey)',
}
const panel = {
  background: 'var(--surface-panel)',
  borderRadius: 'var(--radius-panel)',
}
// The panel is the field: no label, no border, no save button — typing is editing.
const nameField = {
  border: 'none',
  outline: 'none',
  background: 'transparent',
  padding: '0 0 8px',
  borderBottom: '1px solid var(--border-muted)',
  font: '600 17px/1.3 var(--font-ui)',
  color: 'var(--text-primary)',
  width: '100%',
}
const bioField = {
  border: 'none',
  outline: 'none',
  background: 'transparent',
  padding: 0,
  font: 'var(--text-body)',
  color: 'var(--grey)',
  width: '100%',
}

const row = {
  display: 'flex',
  alignItems: 'center',
  gap: '16px',
  padding: '0 28px',
  height: '56px',
  font: 'var(--text-body)',
}

function initials(name) {
  return (name || '').slice(0, 2)
}

const stored = ref({ name: props.me.display_name, bio: props.me.bio || '' })
const justSaved = ref(false)

const dirty = computed(
  () => displayName.value !== stored.value.name || bio.value !== stored.value.bio
)

async function save() {
  if (!dirty.value) return
  error.value = ''
  try {
    await api.updateProfile(displayName.value, bio.value)
    stored.value = { name: displayName.value, bio: bio.value }
    emit('saved')
    justSaved.value = true
    setTimeout(() => (justSaved.value = false), 1500)
  } catch (e) {
    error.value = `${e.message} (${e.status})`
  }
}

async function toggleSessions() {
  sessionsOpen.value = !sessionsOpen.value
  if (sessionsOpen.value) sessions.value = await api.sessions().catch(() => [])
}

async function revoke(id) {
  await api.revokeSession(id).catch(() => null)
  sessions.value = await api.sessions().catch(() => [])
}

function when(iso) {
  return new Date(iso).toLocaleDateString([], { day: 'numeric', month: 'short', year: 'numeric' })
}

toggleSessions()
</script>

<template>
  <section style="flex:1;min-width:0;display:flex;justify-content:center;overflow-y:auto;padding:8px 0">
    <div style="width:620px;display:flex;flex-direction:column;gap:20px">

      <header style="display:flex;align-items:center;gap:16px">
        <h1 style="margin:0;flex:1;font:600 24px/1.2 var(--font-ui);letter-spacing:-0.015em">Profile</h1>
        <SgButton variant="primary" size="sm" :disabled="!dirty" @click="save">
          {{ justSaved ? 'Saved' : 'Save' }}
        </SgButton>
      </header>

      <div :style="panel" style="padding:24px 28px;display:flex;align-items:flex-start;gap:20px">
        <SgAvatar :initials="initials(me.display_name)" :size="44" />

        <div style="flex:1;min-width:0;display:flex;flex-direction:column;gap:10px">
          <input
            v-model="displayName"
            :style="nameField"
            placeholder="Your name"
            @keyup.enter="save"
          >
          <input
            v-model="bio"
            :style="bioField"
            placeholder="A few words about you"
            @keyup.enter="save"
          >
        </div>
      </div>
      <span v-if="error" :style="{ ...mono, color: 'var(--status-error)' }"
            style="padding:0 28px;margin-top:-12px">{{ error }}</span>

      <div :style="panel" style="padding:4px 0">
        <div :style="row">
          <span style="flex:1">Username</span>
          <span :style="handle">@{{ me.username }}</span>
        </div>
        <div style="height:1px;background:var(--border-muted);margin:0 28px"></div>

        <button type="button" :style="{ ...row, width: '100%', border: 'none',
                                        background: 'none', cursor: 'pointer', textAlign: 'left' }"
                @click="toggleSessions">
          <span style="flex:1">Sessions</span>
          <span :style="mono">{{ sessions.length }} devices</span>
        </button>

        <template v-if="sessionsOpen">
          <div v-for="s in sessions" :key="s.id"
               style="display:flex;align-items:center;gap:16px;padding:8px 28px 8px 44px">
            <div style="flex:1;min-width:0">
              <div style="font:var(--text-body);overflow:hidden;text-overflow:ellipsis;white-space:nowrap">
                {{ s.user_agent || 'Unknown device' }}
              </div>
              <div :style="mono">
                {{ s.current ? 'This device' : 'Last active ' + when(s.last_activity_at) }}
              </div>
            </div>
            <SgButton v-if="!s.current" variant="outline" size="sm" @click="revoke(s.id)">Revoke</SgButton>
          </div>
        </template>
      </div>
      <div :style="panel" style="padding:4px 0">
        <button type="button"
                :style="{ ...row, width: '100%', border: 'none', background: 'none',
                          cursor: 'pointer', textAlign: 'left', color: 'var(--red)' }"
                @click="emit('log-out')">
          <span style="flex:1">Log out</span>
        </button>
      </div>

    </div>
  </section>
</template>
