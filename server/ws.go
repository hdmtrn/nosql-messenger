package main

import (
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 30 * time.Second
	sendBuffer = 256
	readLimit  = 512
)

// Sent when the socket's session was revoked or signed out. It is in the range
// RFC 6455 leaves to applications, and it tells the client not to reconnect:
// the next attempt would be refused anyway, and without a code the client
// cannot tell a refusal from a network drop, so it would retry forever.
const closeSessionRevoked = 4001

// The reason travels with its code, so a code added later cannot go out with
// another one's text. The client decides by the code; the reason is for people
// reading logs and devtools.
var closeReasons = map[int]string{
	closeSessionRevoked: "session revoked",
}

// closeFrame is the payload of the close frame; nil for a plain drop.
func closeFrame(code int) []byte {
	if code == 0 {
		return nil
	}
	return websocket.FormatCloseMessage(code, closeReasons[code])
}

// extraOrigins are origins accepted in addition to our own host. Needed for
// one case only: `npm run dev` serves the page from port 5173, while the Vite
// proxy rewrites Host to 8080, so the two never match.
var extraOrigins = strings.FieldsFunc(os.Getenv("WS_ALLOWED_ORIGINS"),
	func(r rune) bool { return r == ',' || r == ' ' })

// checkOrigin refuses cross-site WebSocket hijacking. The browser attaches the
// session cookie to a socket opened from ANY page, and SameSite does not cover
// websockets, so without this check a foreign site could read a user's incoming
// messages. What a socket accepts stays harmless even if this check failed:
// only ephemeral frames such as "typing", which store nothing and answer
// nothing. Anything stored or needing a status code goes through REST.
//
// A request with no Origin is allowed. Only browsers set the header, and only
// browsers attach cookies unprompted; a client that forges it is not a browser
// and gains nothing it could not get by sending the cookie itself. The load
// generator connects this way.
func checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	// A sandboxed iframe or a file:// page sends the literal "null", which
	// parses fine and yields an empty host — hence comparing hosts, not
	// strings, would let it through if Host were ever empty.
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}

	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	return slices.Contains(extraOrigins, origin)
}

var upgrader = websocket.Upgrader{
	CheckOrigin: checkOrigin,
}

func (s *server) handleWS(w http.ResponseWriter, r *http.Request, sess Session) {
	chans, err := s.channels.ForUser(r.Context(), sess.UserID, bson.ObjectID{}, channelsMaxLimit)
	if err != nil {
		log.Printf("listing channels for websocket: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	ids := make([]string, 0, len(chans))
	for _, ch := range chans {
		ids = append(ids, ch.ID.Hex())
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	c := newSubscriber(sess.UserID.Hex())
	c.sessionID = sess.ID.Hex()
	s.hub.Connect(c, ids)

	// A revocation that landed after requireAuth but before Connect found no
	// socket to close. Revocation evicts the cache before it closes sockets, and
	// this asks again only after the socket is in the hub, so one of the two
	// always sees the other. The answer is nearly always a cache hit.
	if _, err := s.sessions.ByToken(r.Context(), sess.Token); err != nil {
		s.hub.Disconnect(c)
		// The write pump has not started, so this goroutine may write.
		conn.SetWriteDeadline(time.Now().Add(writeWait))
		conn.WriteMessage(websocket.CloseMessage, closeFrame(closeSessionRevoked))
		conn.Close()
		return
	}
	log.Printf("+ %s connected (channels: %d)", sess.Username, len(ids))

	go c.writePump(conn)
	c.readPump(conn, func(frame []byte) { s.handleFrame(c, sess, frame) })

	s.hub.Disconnect(c)
	log.Printf("- %s disconnected", sess.Username)
}

// readPump hands every frame to handle on this goroutine, so the frames of one
// connection are handled one at a time and in order, with no lock of their own.
func (c *Subscriber) readPump(conn *websocket.Conn, handle func(frame []byte)) {
	defer conn.Close()

	conn.SetReadLimit(readLimit)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, frame, err := conn.ReadMessage()
		if err != nil {
			return
		}
		handle(frame)
	}
}

func (c *Subscriber) writePump(conn *websocket.Conn) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				conn.WriteMessage(websocket.CloseMessage, closeFrame(c.closeCode))
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}

		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
