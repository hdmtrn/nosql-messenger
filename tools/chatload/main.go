// Command chatload puts conversation traffic on a running messenger: test
// accounts join one channel, each holds a socket open and writes to the channel
// at a steady pace, so that messages cross between nodes through Redis.
//
//	go run ./tools/chatload -base https://chat.example.org -users 10 -duration 5m
//	go run ./tools/chatload -base https://chat.example.org -cleanup
//
// The accounts and their passwords are kept in -accounts between runs, because
// registration is limited per address; -cleanup deletes them and the file.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type account struct {
	Username string `json:"username"`
	Password string `json:"password"`
	token    string
}

// state is what -accounts holds: who was registered and the channel they share.
type state struct {
	Base      string    `json:"base"`
	ChannelID string    `json:"channel_id"`
	Accounts  []account `json:"accounts"`
}

type client struct {
	base string
	http *http.Client
}

func main() {
	base := flag.String("base", "http://localhost:8080", "address of the messenger")
	users := flag.Int("users", 10, "test accounts to use")
	duration := flag.Duration("duration", 3*time.Minute, "how long to write")
	interval := flag.Duration("interval", 2*time.Second, "pause between one account's messages; the server allows 30 per 10 s")
	accountsPath := flag.String("accounts", "chatload-accounts.json", "file keeping the accounts between runs")
	cleanup := flag.Bool("cleanup", false, "delete the accounts in -accounts and the file, then exit")
	flag.Parse()

	c := &client{base: strings.TrimRight(*base, "/"), http: &http.Client{Timeout: 15 * time.Second}}
	st := loadState(*accountsPath, c.base)

	if *cleanup {
		deleteAll(c, st, *accountsPath)
		return
	}
	if *interval < 400*time.Millisecond {
		log.Fatal("an interval under 400 ms runs into the 30-per-10-s message limit")
	}

	for i := len(st.Accounts); i < *users; i++ {
		st.Accounts = append(st.Accounts, account{
			Username: fmt.Sprintf("load-%02d", i+1),
			Password: rand.Text(),
		})
	}
	st.Accounts = st.Accounts[:*users]
	for i := range st.Accounts {
		if err := c.signIn(&st.Accounts[i]); err != nil {
			saveState(*accountsPath, st)
			log.Fatalf("%s: %v", st.Accounts[i].Username, err)
		}
	}
	saveState(*accountsPath, st)
	log.Printf("%d accounts signed in", len(st.Accounts))

	if err := c.shareChannel(st); err != nil {
		log.Fatal(err)
	}
	saveState(*accountsPath, st)
	log.Printf("channel %s, all accounts in it", st.ChannelID)

	run(c, st, *duration, *interval)
}

// signIn logs in, and registers the account the first time.
func (c *client) signIn(a *account) error {
	body := map[string]string{"username": a.Username, "password": a.Password}
	var resp struct {
		Token string `json:"token"`
	}
	status, err := c.do(http.MethodPost, "/auth/login", "", body, &resp)
	if err == nil && status == http.StatusOK {
		a.token = resp.Token
		return nil
	}
	status, err = c.do(http.MethodPost, "/auth/register", "", body, &resp)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return fmt.Errorf("register answered %d", status)
	}
	a.token = resp.Token
	return nil
}

// shareChannel creates the channel on the first run and brings every account
// into it through its invite; following an invite twice is harmless.
func (c *client) shareChannel(st *state) error {
	owner := st.Accounts[0]
	if st.ChannelID == "" {
		var ch struct {
			ID string `json:"id"`
		}
		status, err := c.do(http.MethodPost, "/channels", owner.token, map[string]string{"name": "load test"}, &ch)
		if err != nil || status != http.StatusCreated {
			return fmt.Errorf("creating the channel: %d %v", status, err)
		}
		st.ChannelID = ch.ID
	}
	var inv struct {
		Code string `json:"code"`
	}
	status, err := c.do(http.MethodGet, "/channels/"+st.ChannelID+"/invite", owner.token, nil, &inv)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("reading the invite: %d %v", status, err)
	}
	for _, a := range st.Accounts[1:] {
		status, err := c.do(http.MethodPost, "/invites/"+inv.Code, a.token, nil, nil)
		if err != nil || status >= 300 {
			return fmt.Errorf("%s following the invite: %d %v", a.Username, status, err)
		}
	}
	return nil
}

func run(c *client, st *state, duration, interval time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var sent, refused, failed, received atomic.Int64
	var wg sync.WaitGroup
	for i, a := range st.Accounts {
		conn, err := c.dial(a.token)
		if err != nil {
			log.Fatalf("%s opening the socket: %v", a.Username, err)
		}
		wg.Add(2)
		go func() {
			defer wg.Done()
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
				received.Add(1)
			}
		}()
		go func() {
			defer wg.Done()
			defer conn.Close()
			// Spread the accounts over the interval, so the messages do not
			// all leave in the same instant.
			select {
			case <-time.After(interval * time.Duration(i) / time.Duration(len(st.Accounts))):
			case <-ctx.Done():
				return
			}
			tick := time.NewTicker(interval)
			defer tick.Stop()
			for n := 1; ; n++ {
				msg := map[string]string{
					"channel_id":    st.ChannelID,
					"text":          fmt.Sprintf("load message %d from %s", n, a.Username),
					"client_msg_id": rand.Text(),
				}
				status, err := c.do(http.MethodPost, "/messages", a.token, msg, nil)
				switch {
				case err != nil:
					failed.Add(1)
				case status == http.StatusTooManyRequests:
					refused.Add(1)
				case status >= 300:
					failed.Add(1)
				default:
					sent.Add(1)
				}
				select {
				case <-tick.C:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	report := time.NewTicker(30 * time.Second)
	defer report.Stop()
	for done := false; !done; {
		select {
		case <-report.C:
			log.Printf("sent %d, refused %d, failed %d, frames received %d",
				sent.Load(), refused.Load(), failed.Load(), received.Load())
		case <-ctx.Done():
			done = true
		}
	}
	wg.Wait()
	log.Printf("done: sent %d, refused %d, failed %d, frames received %d",
		sent.Load(), refused.Load(), failed.Load(), received.Load())
}

func (c *client) dial(token string) (*websocket.Conn, error) {
	u, err := url.Parse(c.base + "/ws")
	if err != nil {
		return nil, err
	}
	u.Scheme = strings.Replace(u.Scheme, "http", "ws", 1)
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), http.Header{"Authorization": {"Bearer " + token}})
	return conn, err
}

func deleteAll(c *client, st *state, path string) {
	left := st.Accounts[:0]
	for _, a := range st.Accounts {
		if err := c.signIn(&a); err != nil {
			log.Printf("%s: %v, kept", a.Username, err)
			left = append(left, a)
			continue
		}
		// The server asks for the password again before deleting an account.
		status, err := c.do(http.MethodDelete, "/auth/me", a.token, map[string]string{"password": a.Password}, nil)
		if err != nil || status >= 300 {
			log.Printf("%s: deleting answered %d %v, kept", a.Username, status, err)
			left = append(left, a)
			continue
		}
		log.Printf("%s deleted", a.Username)
	}
	if len(left) > 0 {
		st.Accounts = left
		saveState(path, st)
		log.Fatalf("%d accounts left in %s", len(left), path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Fatal(err)
	}
}

// do sends a JSON request and decodes a JSON answer into out when given.
func (c *client) do(method, path, token string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return 0, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode < 300 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	}
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func loadState(path, base string) *state {
	st := &state{Base: base}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st
	}
	if err != nil {
		log.Fatal(err)
	}
	if err := json.Unmarshal(b, st); err != nil {
		log.Fatalf("%s: %v", path, err)
	}
	// Accounts made on one server mean nothing on another.
	if st.Base != base {
		log.Fatalf("%s holds accounts of %s, not %s; pass another -accounts", path, st.Base, base)
	}
	return st
}

// saveState writes the passwords readable by the owner only.
func saveState(path string, st *state) {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		log.Fatal(err)
	}
}
