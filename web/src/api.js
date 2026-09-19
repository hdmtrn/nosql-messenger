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
    })
  }
  return data
}

export const api = {
  me: () => request('GET', '/auth/me'),
  login: (username, password) => request('POST', '/auth/login', { username, password }),
  register: (username, password) => request('POST', '/auth/register', { username, password }),
  logout: () => request('POST', '/auth/logout'),

  channels: (params) => request('GET', '/channels' + qs(params)),
  createChannel: (name) => request('POST', '/channels', { name }),
  followInvite: (code) => request('POST', `/invites/${encodeURIComponent(code)}`),
  invites: (channelId) => request('GET', `/channels/${channelId}/invites`),
  createInvite: (channelId) => request('POST', `/channels/${channelId}/invites`),
  revokeInvite: (channelId, code) =>
    request('DELETE', `/channels/${channelId}/invites/${encodeURIComponent(code)}`),
  openDirect: (username) => request('POST', '/channels/direct', { username }),
  channel: (id) => request('GET', `/channels/${id}`),
  channelsInCommon: (username) => request('GET', `/users/${encodeURIComponent(username)}/channels`),
  leaveChannel: (id) => request('POST', `/channels/${id}/leave`),
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

  uploadMedia: (file) => upload('/media', file),
  messages: (params) => request('GET', '/messages' + qs(params)),
  send: (message) => request('POST', '/messages', message),
  messagesByIds: (channelId, ids) =>
    request('GET', '/messages' + qs({ channel_id: channelId, ids: ids.join(',') })),
  forward: (id, channelId, clientMsgId) =>
    request('POST', `/messages/${id}/forward`, { channel_id: channelId, client_msg_id: clientMsgId }),
}
