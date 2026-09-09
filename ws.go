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

// extraOrigins are origins accepted in addition to our own host. Needed for
// one case only: `npm run dev` serves the page from port 5173, while the Vite
// proxy rewrites Host to 8080, so the two never match.
var extraOrigins = strings.FieldsFunc(os.Getenv("WS_ALLOWED_ORIGINS"),
	func(r rune) bool { return r == ',' || r == ' ' })

// checkOrigin refuses cross-site WebSocket hijacking. The browser attaches the
// session cookie to a socket opened from ANY page, and SameSite does not cover
// websockets, so without this check a foreign site could read a user's incoming
// messages. Writing is out of reach either way: the socket only ever sends.
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
	s.hub.Connect(c, ids)
	log.Printf("+ %s connected (channels: %d)", sess.Username, len(ids))

	go c.writePump(conn)
	c.readLoop(conn)

	s.hub.Disconnect(c)
	log.Printf("- %s disconnected", sess.Username)
}

func (c *Subscriber) readLoop(conn *websocket.Conn) {
	defer conn.Close()

	conn.SetReadLimit(readLimit)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
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
				conn.WriteMessage(websocket.CloseMessage, nil)
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
