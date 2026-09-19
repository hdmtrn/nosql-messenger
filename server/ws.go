package main

import (
	"context"
	"errors"
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

// Sent when the socket has no session: revoked, signed out, expired, or never
// there. It is in the range RFC 6455 leaves to applications, and it tells the
// client not to reconnect: the next attempt would be refused anyway, and
// without a code the client cannot tell a refusal from a network drop, so it
// would retry forever.
const closeSessionEnded = 4001

// The reason travels with its code, so a code added later cannot go out with
// another one's text. The client decides by the code; the reason is for people
// reading logs and devtools.
var closeReasons = map[int]string{
	closeSessionEnded:                "session ended",
	websocket.CloseInternalServerErr: "internal error",
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

// handleWS checks the session after the upgrade, not before. Refused before
// it, the answer is an HTTP 401 that the browser hides from the page: the
// client sees a close with 1006, the same as a network drop, and reconnects
// forever. Refused after it, the answer is a close code the client acts on.
// Revolt checks the session inside the socket for the same reason, and
// Rocket.Chat's client once looped exactly like ours did.
func (s *server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	sess, err := s.sessions.ByToken(r.Context(), tokenFromRequest(r))
	if err != nil {
		refuse(conn, err)
		return
	}

	chans, err := s.channels.ForUser(r.Context(), sess.UserID, bson.ObjectID{}, channelsMaxLimit)
	if err != nil {
		log.Printf("listing channels for websocket: %v", err)
		closeNow(conn, websocket.CloseInternalServerErr)
		return
	}

	ids := make([]string, 0, len(chans))
	for _, ch := range chans {
		ids = append(ids, ch.ID.Hex())
	}

	c := newSubscriber(sess.UserID.Hex())
	c.sessionID = sess.ID.Hex()
	s.hub.Connect(c, ids)

	// A revocation that landed after the first check but before Connect found
	// no socket to close. Revocation evicts the cache before it closes sockets,
	// and this asks again only after the socket is in the hub, so one of the two
	// always sees the other. The answer is nearly always a cache hit.
	if _, err := s.sessions.ByToken(r.Context(), sess.Token); err != nil {
		s.hub.Disconnect(c)
		refuse(conn, err)
		return
	}
	log.Printf("+ %s connected (channels: %d)", sess.Username, len(ids))

	// The socket authenticated once, above; the session can expire while it
	// stays open, and nothing else would notice — expiry is seen only by a
	// request that looks the session up, and the TTL index deletes the document
	// without a word. The write pump asks again when the session is due.
	recheck := func() (time.Time, error) {
		ctx, cancel := context.WithTimeout(context.Background(), writeWait)
		defer cancel()
		fresh, err := s.sessions.ByToken(ctx, sess.Token)
		return fresh.ExpiresAt, err
	}
	go c.writePump(conn, sess.ExpiresAt, recheck)
	c.readPump(conn, func(frame []byte) { s.handleFrame(c, sess, frame) })

	s.hub.Disconnect(c)
	log.Printf("- %s disconnected", sess.Username)
}

// refuse closes a socket whose session could not be confirmed. Only a session
// that is really gone ends in 4001 and the sign-in screen; a database that did
// not answer is a fault of ours, and the client should simply come back.
func refuse(conn *websocket.Conn, err error) {
	if errors.Is(err, errSessionNotFound) {
		closeNow(conn, closeSessionEnded)
		return
	}
	log.Printf("checking the session of a websocket: %v", err)
	closeNow(conn, websocket.CloseInternalServerErr)
}

// closeNow is for a socket whose write pump has not started, so this
// goroutine may write to it.
func closeNow(conn *websocket.Conn, code int) {
	conn.SetWriteDeadline(time.Now().Add(writeWait))
	conn.WriteMessage(websocket.CloseMessage, closeFrame(code))
	conn.Close()
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

// writePump is the only goroutine that writes to the socket. Besides the
// messages and pings, it closes the socket when its session runs out: a timer
// set to the session's expiry asks recheck again, since activity elsewhere may
// have extended it. Mattermost keeps the expiry on the connection the same way
// and re-reads the session once it has passed; it stops sending, we close.
func (c *Subscriber) writePump(conn *websocket.Conn, expiresAt time.Time, recheck func() (time.Time, error)) {
	ticker := time.NewTicker(pingPeriod)
	expiry := time.NewTimer(time.Until(expiresAt))
	defer func() {
		ticker.Stop()
		expiry.Stop()
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

		case <-expiry.C:
			next, err := recheck()
			if errors.Is(err, errSessionNotFound) {
				conn.SetWriteDeadline(time.Now().Add(writeWait))
				conn.WriteMessage(websocket.CloseMessage, closeFrame(closeSessionEnded))
				return
			}
			if err != nil {
				// The database did not answer; that is no reason to sign
				// anyone out. Ask again on the next ping's schedule.
				log.Printf("rechecking the session of a websocket: %v", err)
				next = time.Now().Add(pingPeriod)
			}
			expiry.Reset(time.Until(next))
		}
	}
}
