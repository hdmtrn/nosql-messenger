import { reactive } from 'vue'
import { api } from './api'

// The other member of a direct channel, or null once that account is deleted:
// erasing an account takes it out of every conversation it was in.
export function otherMember(channel, meId) {
  return (channel?.members || []).find((m) => m.user_id !== meId) || null
}

// A direct channel has no name of its own: it is displayed as the other person.
export function channelTitle(channel, meId) {
  if (!channel) return ''
  if (channel.kind !== 'direct') return channel.name
  const other = otherMember(channel, meId)
  return other ? other.username : DELETED_NAME
}

// Unlike a person's, a channel's picture id comes with the channel itself.
export function channelAvatarUrl(channel) {
  return channel && channel.avatar_id ? `/media/${channel.avatar_id}` : ''
}

// First letter of the first word and of the last one, so "Anna Maria Petrova" is AP.
// Spreading the string walks code points: slice() would cut an emoji in half.
export function initials(name) {
  const words = (name || '').trim().split(/\s+/).filter(Boolean)
  if (!words.length) return ''
  const first = [...words[0]][0]
  if (words.length === 1) return first
  return first + [...words[words.length - 1]][0]
}

// Documents embed the username only, because the display name and the avatar
// change. The client looks each person up once per session instead, and a
// reactive map re-renders whoever asked as soon as the answer arrives.
const people = reactive(new Map())
const asked = new Set()

function lookUp(username) {
  if (asked.has(username)) return
  asked.add(username)
  api.user(username)
    .then(rememberUser)
    .catch(() => people.set(username, { name: username, avatar: '' }))
}

export function displayName(username) {
  if (!username) return ''
  lookUp(username)
  return people.get(username)?.name || username
}

// Empty while the person is unknown or has no picture: the avatar shows initials.
export function avatarUrl(username) {
  if (!username) return ''
  lookUp(username)
  const id = people.get(username)?.avatar
  return id ? `/media/${id}` : ''
}

export const DELETED_NAME = 'Deleted Account'
// It stands as the title of a conversation whose other side is gone, and names
// nobody to look up: no username has a space in it.
people.set(DELETED_NAME, { name: DELETED_NAME, avatar: '' })
asked.add(DELETED_NAME)

// For answers that already carry the user: our own profile, search results.
// A deleted account keeps its messages, and they read as from no one in particular.
export function rememberUser(u) {
  if (!u || !u.username) return
  asked.add(u.username)
  if (u.deleted) {
    people.set(u.username, { name: DELETED_NAME, avatar: '' })
    return
  }
  people.set(u.username, { name: u.display_name || u.username, avatar: u.avatar_id || '' })
}

// What a message reads as in a quote or a pending reply: its text, or a word for
// the pictures when it has none.
export function messagePreview(m) {
  if (!m) return ''
  if (m.text) return m.text
  const n = (m.attachments || []).length
  return n > 1 ? `${n} photos` : n ? 'Photo' : ''
}
