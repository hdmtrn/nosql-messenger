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
  joinChannel: (code) => request('POST', '/channels/join', { code }),
  leaveChannel: (id) => request('POST', `/channels/${id}/leave`),

  friendRequests: () => request('GET', '/friends/requests'),
  sendFriendRequest: (username) => request('POST', '/friends/requests', { username }),
  acceptFriendRequest: (id) => request('POST', `/friends/requests/${id}/accept`),
  declineFriendRequest: (id) => request('POST', `/friends/requests/${id}/decline`),
  friends: () => request('GET', '/friends'),

  messages: (params) => request('GET', '/messages' + qs(params)),
  send: (message) => request('POST', '/messages', message),
}
