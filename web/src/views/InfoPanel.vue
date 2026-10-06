<script setup>
import { computed, ref, watch } from 'vue'
import { api } from '../api'
import ChannelGlyph from '../components/ChannelGlyph.vue'
import SgAvatar from '../components/SgAvatar.vue'
import SgButton from '../components/SgButton.vue'
import SgDialog from '../components/SgDialog.vue'
import MediaViewer from '../components/MediaViewer.vue'
import RemoveFriendDialog from '../components/RemoveFriendDialog.vue'
import { avatarUrl, channelAvatarUrl, displayName, initials, otherMember } from '../naming'
import { toJpeg } from '../images'
import { inviteLink } from '../pending'

const props = defineProps({
  me: { type: Object, required: true },
  channel: { type: Object, required: true },
  title: { type: String, required: true },
  // Of a direct conversation: whether its other side is a friend, which the
  // messenger knows from its lists.
  friend: Boolean,
})
const emit = defineEmits(['close', 'leave', 'select', 'person', 'changed', 'unfriend'])

const direct = () => props.channel.kind === 'direct'
// A direct conversation whose other side deleted the account: nobody to look up.
const alone = () => direct() && !otherMember(props.channel, props.me.id)

const members = ref([])
const inviteCode = ref('')
const person = ref(null)
const common = ref([])
const confirmRemove = ref(false)

// Every load is numbered. Switching channels twice in a row leaves two requests
// in flight, and the first one may answer last; a reply that is not the newest
// question's is dropped instead of overwriting it.
let asked = 0

async function load() {
  const mine = ++asked
  members.value = []
  inviteCode.value = ''
  inviteError.value = ''
  person.value = null
  common.value = []

  if (alone()) return
  if (direct()) {
    const [who, shared] = await Promise.all([
      api.user(props.title).catch(() => null),
      api.channelsInCommon(props.title).catch(() => []),
    ])
    if (mine !== asked) return
    person.value = who
    common.value = shared
  } else {
    const [full, link] = await Promise.all([
      api.channel(props.channel.id).catch(() => null),
      api.invite(props.channel.id).catch(() => null),
    ])
    if (mine !== asked) return
    members.value = full ? full.members || [] : []
    inviteCode.value = link ? link.code : ''
  }
}

const copied = ref(false)

function copyLink() {
  navigator.clipboard.writeText(inviteLink(inviteCode.value))
  copied.value = true
  setTimeout(() => (copied.value = false), 1500)
}

// A reset cannot be undone: everyone holding the old link is shut out of it.
const confirmReset = ref(false)
const inviteError = ref('')

async function resetLink() {
  confirmReset.value = false
  inviteError.value = ''
  const mine = asked
  try {
    const link = await api.resetInvite(props.channel.id)
    if (mine === asked) inviteCode.value = link.code
  } catch (e) {
    if (mine === asked) inviteError.value = e.message
  }
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

</script>

<template>
  <aside class="info-panel">
    <div class="top">
      <h2 class="heading">
        {{ direct() ? 'About' : 'Channel' }}
      </h2>
      <SgButton variant="outline" size="sm" @click="emit('close')">Close</SgButton>
    </div>

    <div class="card summary">
      <SgAvatar v-if="direct()" :initials="initials(displayName(title))" :src="avatarUrl(title)" :size="64" />
      <template v-else>
        <button type="button" class="avatar" :class="{ busy: uploading }" :disabled="!channel.avatar_id"
                :aria-label="channel.avatar_id ? 'Open photo' : undefined" @click="viewing = true">
          <ChannelGlyph :src="channelAvatarUrl(channel)" :size="64" tone="blue" />
        </button>
        <div v-if="isOwner" class="owner-actions">
          <SgButton variant="outline" size="sm" :disabled="uploading" @click="picker.click()">
            {{ uploading ? 'Uploading' : 'Edit' }}
          </SgButton>
          <button v-if="channel.avatar_id" type="button" class="label remove"
                  @click="removeAvatar">Remove</button>
          <input ref="picker" type="file" accept="image/*" hidden @change="changeAvatar">
        </div>
        <p v-if="avatarError" class="label note error">{{ avatarError }}</p>
        <MediaViewer v-if="viewing && channel.avatar_id"
                     :pictures="[{ key: channel.avatar_id, url: channelAvatarUrl(channel) }]"
                     @close="viewing = false" />
      </template>

      <div>
        <!-- The title of a direct channel is the other person's handle; what is read is their name. -->
        <div class="name">{{ direct() ? displayName(title) : title }}</div>
        <div v-if="direct() && !alone()" class="handle below">@{{ title }}</div>
        <div v-else-if="!direct()" class="label below">{{ channel.member_count }} members</div>
      </div>

      <p v-if="direct() && person && person.bio" class="bio">{{ person.bio }}</p>

    </div>

    <template v-if="direct()">
      <span class="label section">Channels in common</span>
      <div class="card list">
        <p v-if="!common.length" class="row muted">None</p>
        <button v-for="c in common" :key="c.id" type="button" class="row clickable body"
                @click="emit('select', c.id)">
          <ChannelGlyph :src="channelAvatarUrl(c)" :size="22" tone="blue" />
          <span class="grow">{{ c.name }}</span>
        </button>
      </div>

      <div v-if="friend" class="card list">
        <button type="button" class="row clickable body leave"
                @click="confirmRemove = true">Remove friend</button>
      </div>
      <RemoveFriendDialog v-if="confirmRemove" :name="displayName(title)" @close="confirmRemove = false"
                          @remove="confirmRemove = false; emit('unfriend', title)" />
    </template>

    <template v-else>
      <span class="label section">Invite link</span>
      <div class="card invite">
        <p v-if="inviteError" class="label note error">{{ inviteError }}</p>
        <div class="invite-actions">
          <SgButton variant="outline" size="sm" :disabled="!inviteCode" @click="copyLink">
            {{ copied ? 'Copied' : 'Copy invite link' }}
          </SgButton>
          <SgButton v-if="isOwner" variant="mutedText" @click="confirmReset = true">Reset link</SgButton>
        </div>
      </div>

      <span class="label section">Members</span>
      <div class="card list">
        <component :is="m.user_id === me.id ? 'div' : 'button'" v-for="m in members" :key="m.user_id"
                   :type="m.user_id === me.id ? undefined : 'button'"
                   class="row" :class="{ clickable: m.user_id !== me.id }"
                   @click="m.user_id === me.id || emit('person', m.username)">
          <SgAvatar :initials="initials(displayName(m.username))" :src="avatarUrl(m.username)" :size="28" />
          <span class="grow body">{{ displayName(m.username) }}</span>
          <span class="label">{{ m.user_id === me.id ? 'You' : m.role === 'owner' ? 'Owner' : '' }}</span>
        </component>
      </div>

      <div class="card list">
        <button type="button" class="row clickable body leave"
                @click="emit('leave')">Leave channel</button>
      </div>

      <!-- The stage is a size container, which would pin a fixed dialog to it. -->
      <Teleport to="body">
        <SgDialog v-if="confirmReset" title="Reset invite link?" @close="confirmReset = false">
          <p class="dialog-text">
            The current link stops working, and nobody can join with it any more. Members get the new one here.
          </p>
          <div class="dialog-actions">
            <SgButton variant="danger" @click="resetLink">Reset</SgButton>
            <SgButton variant="outline" @click="confirmReset = false">Cancel</SgButton>
          </div>
        </SgDialog>
      </Teleport>
    </template>
  </aside>
</template>

<style scoped>
.info-panel {
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
.summary {
  padding: 24px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  text-align: center;
}
.owner-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}
.name { font: 600 20px/1.2 var(--font-ui); }
.handle {
  font: var(--text-meta);
  color: var(--grey);
}
.below { margin-top: 4px; }
.bio {
  margin: 0;
  font: var(--text-body);
  color: var(--text-muted);
}
.label {
  font: var(--text-label);
  color: var(--grey);
}
.note { margin: 0; }
.error { color: var(--status-error); }
.section { padding: 0 24px; }
.list { padding: 4px 0; }
.invite {
  padding: 12px 24px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.invite-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.dialog-text {
  margin: 0;
  font: var(--text-body);
  color: var(--text-muted);
}
.dialog-actions {
  display: flex;
  gap: 12px;
}
.row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 0 24px;
  height: 52px;
}
.muted { color: var(--text-muted); }
.clickable {
  width: 100%;
  border: none;
  background: none;
  cursor: pointer;
}
.body { font: var(--text-body); }
.leave { color: var(--red); }
.grow {
  flex: 1;
  text-align: left;
}
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
.remove:hover { color: var(--status-error); }
</style>
