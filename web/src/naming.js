import { reactive } from 'vue'
import { api } from './api'

// A direct channel has no name of its own: it is displayed as the other person.
export function channelTitle(channel, meId) {
  if (!channel) return ''
  if (channel.kind !== 'direct') return channel.name
  const other = (channel.members || []).find((m) => m.user_id !== meId)
  return other ? other.username : 'Direct message'
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

// Documents embed the username only, because the display name changes. The client
// looks each person up once per session instead, and a reactive map re-renders
// whoever asked as soon as the answer arrives.
const names = reactive(new Map())
const asked = new Set()

export function displayName(username) {
  if (!username) return ''
  if (names.has(username)) return names.get(username)
  if (!asked.has(username)) {
    asked.add(username)
    api.user(username)
      .then((u) => names.set(username, u.display_name || username))
      .catch(() => names.set(username, username))
  }
  return username
}

// For answers that already carry the name: our own profile, search results.
export function rememberName(username, name) {
  if (username && name) names.set(username, name)
}
