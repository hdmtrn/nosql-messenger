// An invite arrives as a link, so the code lives in the address bar for exactly
// as long as it takes to read it. It is kept here rather than acted on at once,
// because whoever followed the link may still have to sign in first.
let code = null

const match = location.pathname.match(/^\/invite\/(.+)$/)
if (match) {
  code = decodeURIComponent(match[1])
  history.replaceState(null, '', '/')
}

export function takePendingInvite() {
  const value = code
  code = null
  return value
}

export function inviteLink(inviteCode) {
  return `${location.origin}/invite/${encodeURIComponent(inviteCode)}`
}
