<script setup>
import { ref } from 'vue'
import { api } from '../api'
import SgAvatar from '../components/SgAvatar.vue'
import SgButton from '../components/SgButton.vue'
import SgInput from '../components/SgInput.vue'

const props = defineProps({ me: { type: Object, required: true } })
const emit = defineEmits(['close', 'saved', 'log-out'])

const displayName = ref(props.me.display_name)
const bio = ref(props.me.bio || '')
const error = ref('')
const busy = ref(false)

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

async function save() {
  error.value = ''
  busy.value = true
  try {
    await api.updateProfile(displayName.value, bio.value)
    emit('saved')
  } catch (e) {
    error.value = `${e.message} (${e.status})`
  } finally {
    busy.value = false
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
        <SgButton variant="outline" size="sm" @click="emit('close')">Close</SgButton>
      </header>

      <div :style="panel" style="padding:24px 28px;display:flex;align-items:center;gap:20px">
        <SgAvatar :initials="initials(me.display_name)" :size="44" />
        <div style="flex:1;display:flex;flex-direction:column;gap:4px">
          <span style="font:600 17px/1.3 var(--font-ui)">{{ me.username }}</span>
          <span :style="mono">Member since {{ new Date(me.created_at).getFullYear() }}</span>
        </div>
      </div>

      <SgInput v-model="displayName" label="Display name" hint="Shown next to your messages"
               :error="error" />
      <SgInput v-model="bio" label="Bio" hint="Any details such as role or city" />

      <div style="display:flex;gap:12px">
        <SgButton variant="primary" :disabled="busy" @click="save">Save</SgButton>
      </div>

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
      <span :style="mono" style="padding:0 28px;margin-top:-12px">
        Your username is how people find you
      </span>

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
