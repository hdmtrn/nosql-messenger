function qs(params) {
  const s = new URLSearchParams(
    Object.entries(params || {}).filter(([, v]) => v !== undefined && v !== null && v !== '')
  ).toString()
  return s ? '?' + s : ''
}

async function request(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  })
  return parse(res)
}

// Files go as the raw body: the server streams it straight into storage.
async function upload(path, blob) {
  return parse(await fetch(path, { method: 'POST', headers: { 'Content-Type': blob.type || 'application/octet-stream' }, body: blob }))
}

async function parse(res) {
  const text = await res.text()
  const data = text ? JSON.parse(text) : null
  if (!res.ok) {
    throw Object.assign(new Error((data && data.error) || res.statusText), {
      status: res.status,
      // Seconds until a 429 lifts, from the header the server sets with it.
      retryAfter: Number(res.headers.get('Retry-After')) || 0,
    })
  }
  return data
}

// How long a 429 asks to wait, as a person reads it.
export function waitText(seconds) {
  if (seconds < 60) return `${seconds} s`
  if (seconds < 3600) return `${Math.ceil(seconds / 60)} min`
  return `${Math.ceil(seconds / 3600)} h`
}

// A send or an upload refused for coming too fast waits out the limit and goes
// again: the message stays "Sending" rather than failing over a pause the server
// itself names. A wait longer than a person would sit through fails as usual.
export async function patiently(call, longest = 60) {
  for (;;) {
    try {
      return await call()
    } catch (e) {
      if (e.status !== 429 || !e.retryAfter || e.retryAfter > longest) throw e
      await new Promise((resolve) => setTimeout(resolve, e.retryAfter * 1000))
    }
  }
}

export const api = {
  me: () => request('GET', '/auth/me'),
  login: (username, password) => request('POST', '/auth/login', { username, password }),
  register: (username, password) => request('POST', '/auth/register', { username, password }),
  logout: () => request('POST', '/auth/logout'),
  deleteAccount: (password) => request('DELETE', '/auth/me', { password }),

  channels: (params) => request('GET', '/channels' + qs(params)),
  createChannel: (name) => request('POST', '/channels', { name }),
  followInvite: (code) => request('POST', `/invites/${encodeURIComponent(code)}`),
  invite: (channelId) => request('GET', `/channels/${channelId}/invite`),
  resetInvite: (channelId) => request('POST', `/channels/${channelId}/invite/reset`),
  openDirect: (username) => request('POST', '/channels/direct', { username }),
  channel: (id) => request('GET', `/channels/${id}`),
  channelsInCommon: (username) => request('GET', `/users/${encodeURIComponent(username)}/channels`),
  leaveChannel: (id) => request('POST', `/channels/${id}/leave`),
  markRead: (id, seq) => request('POST', `/channels/${id}/read`, { seq }),
  setChannelAvatar: (id, blob) => upload(`/channels/${id}/avatar`, blob),
  removeChannelAvatar: (id) => request('DELETE', `/channels/${id}/avatar`),

  presence: (ids) => request('GET', '/presence' + qs({ ids: ids.join(',') })),

  searchUsers: (q) => request('GET', '/users' + qs({ q })),
  user: (username) => request('GET', `/users/${encodeURIComponent(username)}`),
  updateProfile: (display_name, bio) => request('POST', '/auth/me/profile', { display_name, bio }),
  setAvatar: (blob) => upload('/auth/me/avatar', blob),
  removeAvatar: () => request('DELETE', '/auth/me/avatar'),
  sessions: () => request('GET', '/auth/sessions'),
  revokeSession: (id) => request('DELETE', `/auth/sessions/${id}`),

  friendRequests: () => request('GET', '/friends/requests'),
  sendFriendRequest: (username) => request('POST', '/friends/requests', { username }),
  acceptFriendRequest: (id) => request('POST', `/friends/requests/${id}/accept`),
  declineFriendRequest: (id) => request('POST', `/friends/requests/${id}/decline`),
  friends: () => request('GET', '/friends'),
  removeFriend: (id) => request('DELETE', `/friends/${id}`),

  uploadMedia: (file) => upload('/media', file),
  messages: (params) => request('GET', '/messages' + qs(params)),
  send: (message) => request('POST', '/messages', message),
  messagesByIds: (channelId, ids) =>
    request('GET', '/messages' + qs({ channel_id: channelId, ids: ids.join(',') })),
  forward: (id, channelId, clientMsgId) =>
    request('POST', `/messages/${id}/forward`, { channel_id: channelId, client_msg_id: clientMsgId }),
}
