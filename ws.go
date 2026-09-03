package main

import (
	"log"
	"net/http"
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

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
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
