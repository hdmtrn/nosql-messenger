// A direct channel has no name of its own: it is displayed as the other person.
export function channelTitle(channel, meId) {
  if (!channel) return ''
  if (channel.kind !== 'direct') return channel.name
  const other = (channel.members || []).find((m) => m.user_id !== meId)
  return other ? other.username : 'Direct message'
}

export function initials(name) {
  return (name || '').slice(0, 2)
}
