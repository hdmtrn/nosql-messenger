const RETRY_MIN = 1000
const RETRY_MAX = 15000
// The server closes with this when the socket's session was revoked or signed
// out. Reconnecting would only be refused, and a refused handshake looks like a
// network drop from here, so without the code the tab would retry forever.
const SESSION_REVOKED = 4001

export function createSocket({ onMessage, onStateChange, onSessionEnded }) {
  let ws = null
  let retry = RETRY_MIN
  let timer = null
  let closed = false

  function connect() {
    if (closed) return
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    ws = new WebSocket(`${proto}//${location.host}/ws`)

    ws.onopen = () => {
      retry = RETRY_MIN
      onStateChange('online')
    }

    ws.onmessage = (e) => {
      try {
        onMessage(JSON.parse(e.data))
      } catch {
        /* a frame we do not understand is not worth killing the socket over */
      }
    }

    ws.onclose = (e) => {
      onStateChange('offline')
      if (closed) return
      if (e.code === SESSION_REVOKED) {
        closed = true
        onSessionEnded()
        return
      }
      timer = setTimeout(connect, retry)
      retry = Math.min(retry * 2, RETRY_MAX)
    }

    ws.onerror = () => ws.close()
  }

  connect()

  return {
    // Only ephemeral frames go this way, so one sent while the socket is down is
    // simply lost; anything that must arrive goes through REST.
    send(data) {
      if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(data))
    },
    close() {
      closed = true
      clearTimeout(timer)
      ws && ws.close()
    },
  }
}
