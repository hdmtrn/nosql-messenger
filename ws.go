package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 30 * time.Second
	sendBuffer = 256
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type outMsg struct {
	From string `json:"from"`
	Text string `json:"text"`
	At   string `json:"at"`
}

func handleWS(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := r.URL.Query().Get("user")
		chID := r.URL.Query().Get("channel")
		if userID == "" || chID == "" {
			http.Error(w, "user and channel parameters are required", http.StatusBadRequest)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		c := &Subscriber{userID: userID, send: make(chan []byte, sendBuffer)}

		n := hub.register(chID, c)
		log.Printf("+ %s joined %s (subscribers: %d)", userID, chID, n)

		go c.writePump(conn)

		c.readPump(conn, hub, chID)

		n = hub.unregister(chID, c)
		log.Printf("- %s left %s (subscribers: %d)", userID, chID, n)
	}
}

func (c *Subscriber) readPump(conn *websocket.Conn, hub *Hub, chID string) {
	defer conn.Close()

	conn.SetReadLimit(4096)
	conn.SetReadDeadline(time.Now().Add(pongWait))

	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		out, err := json.Marshal(outMsg{
			From: c.userID,
			Text: string(data),
			At:   time.Now().Format(time.RFC3339),
		})
		if err != nil {
			continue
		}
		hub.Publish(chID, out)
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
			if !ok {
				conn.SetWriteDeadline(time.Now().Add(writeWait))
				conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			conn.SetWriteDeadline(time.Now().Add(writeWait))
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
