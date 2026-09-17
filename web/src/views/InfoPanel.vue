<script setup>
import { computed, ref, watch } from 'vue'
import { api } from '../api'
import ChannelGlyph from '../components/ChannelGlyph.vue'
import SgAvatar from '../components/SgAvatar.vue'
import SgButton from '../components/SgButton.vue'
import MediaViewer from '../components/MediaViewer.vue'
import { avatarUrl, channelAvatarUrl, displayName, initials } from '../naming'
import { toJpeg } from '../images'
import { inviteLink } from '../pending'

const props = defineProps({
  me: { type: Object, required: true },
  channel: { type: Object, required: true },
  title: { type: String, required: true },
})
const emit = defineEmits(['close', 'leave', 'select', 'person', 'changed'])

const direct = () => props.channel.kind === 'direct'

const members = ref([])
const invites = ref([])
const person = ref(null)
const common = ref([])

// Every load is numbered. Switching channels twice in a row leaves two requests
// in flight, and the first one may answer last; a reply that is not the newest
// question's is dropped instead of overwriting it.
let asked = 0

async function load() {
  const mine = ++asked
  members.value = []
  invites.value = []
  person.value = null
  common.value = []

  if (direct()) {
    const [who, shared] = await Promise.all([
      api.user(props.title).catch(() => null),
      api.channelsInCommon(props.title).catch(() => []),
    ])
    if (mine !== asked) return
    person.value = who
    common.value = shared
  } else {
    const [full, codes] = await Promise.all([
      api.channel(props.channel.id).catch(() => null),
      api.invites(props.channel.id).catch(() => []),
    ])
    if (mine !== asked) return
    members.value = full ? full.members || [] : []
    invites.value = codes
  }
}

const copiedCode = ref('')

function made(iso) {
  return new Date(iso).toLocaleDateString([], { day: 'numeric', month: 'short' })
}

function copy(code) {
  navigator.clipboard.writeText(inviteLink(code))
  copiedCode.value = code
  setTimeout(() => (copiedCode.value = ''), 1500)
}

// The server caps invites at the number this list shows, so New can legitimately
// refuse. A button that does nothing and says nothing is worse than the cap.
const inviteError = ref('')

async function newInvite() {
  inviteError.value = ''
  try {
    await api.createInvite(props.channel.id)
  } catch (e) {
    inviteError.value = e.message
  }
  invites.value = await api.invites(props.channel.id).catch(() => [])
}

async function revoke(code) {
  inviteError.value = ''
  await api.revokeInvite(props.channel.id, code).catch(() => null)
  invites.value = await api.invites(props.channel.id).catch(() => [])
}

watch(() => props.channel.id, load, { immediate: true })

// The server decides who may change the picture; the buttons only follow it.
const isOwner = computed(() =>
  members.value.some((m) => m.user_id === props.me.id && m.role === 'owner'))

// Same size and square crop as a person's avatar, for the same 1 MB limit.
const AVATAR_SIDE = 640
const picker = ref(null)
const uploading = ref(false)
const viewing = ref(false)
const avatarError = ref('')

async function changeAvatar(event) {
  const file = event.target.files[0]
  event.target.value = ''
  if (!file) return
  avatarError.value = ''
  uploading.value = true
  try {
    await api.setChannelAvatar(props.channel.id, await toJpeg(file, { side: AVATAR_SIDE, square: true }))
    emit('changed')
  } catch (e) {
    avatarError.value = e.status ? `${e.message} (${e.status})` : 'This file is not an image'
  } finally {
    uploading.value = false
  }
}

async function removeAvatar() {
  avatarError.value = ''
  try {
    await api.removeChannelAvatar(props.channel.id)
    emit('changed')
  } catch (e) {
    avatarError.value = `${e.message} (${e.status})`
  }
}

const mono = {
  font: 'var(--text-machine)',
  textTransform: 'uppercase',
  letterSpacing: 'var(--mono-tracking)',
  color: 'var(--grey)',
}
const handle = { font: 'var(--text-machine-11)', letterSpacing: 'var(--mono-tracking)', color: 'var(--grey)' }
const panel = { background: 'var(--surface-panel)', borderRadius: 'var(--radius-panel)' }
const row = { display: 'flex', alignItems: 'center', gap: '12px', padding: '0 24px', height: '52px' }
</script>

<template>
  <aside style="flex:0 0 340px;display:flex;flex-direction:column;gap:16px;overflow-y:auto">
    <div style="display:flex;align-items:center;gap:12px">
      <h2 style="margin:0;flex:1;font:600 20px/1.2 var(--font-ui)">
        {{ direct() ? 'About' : 'Channel' }}
      </h2>
      <SgButton variant="outline" size="sm" @click="emit('close')">Close</SgButton>
    </div>

    <div :style="panel" style="padding:24px;display:flex;flex-direction:column;
                               align-items:center;gap:12px;text-align:center">
      <SgAvatar v-if="direct()" :initials="initials(displayName(title))" :src="avatarUrl(title)" :size="64" />
      <template v-else>
        <button type="button" class="avatar" :class="{ busy: uploading }" :disabled="!channel.avatar_id"
                :aria-label="channel.avatar_id ? 'Open photo' : undefined" @click="viewing = true">
          <ChannelGlyph :src="channelAvatarUrl(channel)" :size="64" tone="blue" />
        </button>
        <div v-if="isOwner" style="display:flex;align-items:center;gap:12px">
          <SgButton variant="outline" size="sm" :disabled="uploading" @click="picker.click()">
            {{ uploading ? 'Uploading' : 'Edit' }}
          </SgButton>
          <button v-if="channel.avatar_id" type="button" class="remove" :style="mono"
                  @click="removeAvatar">Remove</button>
          <input ref="picker" type="file" accept="image/*" hidden @change="changeAvatar">
        </div>
        <p v-if="avatarError" :style="mono" style="margin:0;color:var(--status-error)">{{ avatarError }}</p>
        <MediaViewer v-if="viewing && channel.avatar_id"
                     :pictures="[{ key: channel.avatar_id, url: channelAvatarUrl(channel) }]"
                     @close="viewing = false" />
      </template>

      <div>
        <!-- The title of a direct channel is the other person's handle; what is read is their name. -->
        <div style="font:600 20px/1.2 var(--font-ui)">{{ direct() ? displayName(title) : title }}</div>
        <div v-if="direct()" :style="handle" style="margin-top:4px">@{{ title }}</div>
        <div v-else :style="mono" style="margin-top:4px">{{ channel.member_count }} members</div>
      </div>

      <p v-if="direct() && person && person.bio"
         style="margin:0;font:var(--text-body);color:var(--text-muted)">{{ person.bio }}</p>

    </div>

    <template v-if="direct()">
      <span :style="mono" style="padding:0 24px">Channels in common</span>
      <div :style="panel" style="padding:4px 0">
        <p v-if="!common.length" :style="row" style="color:var(--text-muted)">None</p>
        <button v-for="c in common" :key="c.id" type="button"
                :style="{ ...row, width: '100%', border: 'none', background: 'none',
                          cursor: 'pointer', font: 'var(--text-body)' }"
                @click="emit('select', c.id)">
          <ChannelGlyph :src="channelAvatarUrl(c)" :size="22" tone="blue" />
          <span style="flex:1;text-align:left">{{ c.name }}</span>
        </button>
      </div>
    </template>

    <template v-else>
      <div style="display:flex;align-items:center;padding:0 24px">
        <span :style="mono" style="flex:1">Invites</span>
        <SgButton variant="outline" size="sm" @click="newInvite">New</SgButton>
      </div>
      <div :style="panel" style="padding:12px 24px;display:flex;flex-direction:column;gap:10px">
        <p v-if="inviteError" :style="mono" style="margin:0;color:var(--status-error)">
          {{ inviteError }}
        </p>
        <p v-if="!invites.length" :style="mono" style="margin:0">None</p>
        <div v-for="i in invites" :key="i.code" style="display:flex;align-items:center;gap:12px">
          <SgButton variant="outline" size="sm" @click="copy(i.code)">
            {{ copiedCode === i.code ? 'Copied' : 'Copy invite link' }}
          </SgButton>
          <span :style="mono" style="flex:1">{{ made(i.created_at) }}</span>
          <SgButton variant="mutedText" @click="revoke(i.code)">Revoke</SgButton>
        </div>
      </div>

      <span :style="mono" style="padding:0 24px">Members</span>
      <div :style="panel" style="padding:4px 0">
        <component :is="m.user_id === me.id ? 'div' : 'button'" v-for="m in members" :key="m.user_id"
                   :type="m.user_id === me.id ? undefined : 'button'"
                   :style="m.user_id === me.id ? row
                     : { ...row, width: '100%', border: 'none', background: 'none', cursor: 'pointer' }"
                   @click="m.user_id === me.id || emit('person', m.username)">
          <SgAvatar :initials="initials(displayName(m.username))" :src="avatarUrl(m.username)" :size="28" />
          <span style="flex:1;text-align:left;font:var(--text-body)">{{ displayName(m.username) }}</span>
          <span :style="mono">{{ m.user_id === me.id ? 'You' : m.role === 'owner' ? 'Owner' : '' }}</span>
        </component>
      </div>

      <div :style="panel" style="padding:4px 0">
        <button type="button"
                :style="{ ...row, width: '100%', border: 'none', background: 'none',
                          cursor: 'pointer', font: 'var(--text-body)', color: 'var(--red)' }"
                @click="emit('leave')">Leave channel</button>
      </div>
    </template>
  </aside>
</template>

<style scoped>
.avatar {
  padding: 0;
  border: none;
  background: none;
  border-radius: var(--radius-pill);
  cursor: zoom-in;
}
.avatar:disabled { cursor: default; }
.avatar.busy { opacity: 0.5; }
.remove {
  padding: 0;
  border: none;
  background: none;
  cursor: pointer;
}
.remove:hover { color: var(--status-error) !important; }
</style>
