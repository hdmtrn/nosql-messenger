package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// testLimiter counts under a prefix of its own: the tests share one Redis with
// each other and with whatever stack runs on the machine.
func testLimiter(t *testing.T) *limiter {
	t.Helper()

	if testing.Short() {
		t.Skip("integration test: needs Redis, skipped under -short")
	}

	ctx := context.Background()
	rdb := redis.NewClient(redisOptions())
	if err := rdb.Ping(ctx).Err(); err != nil {
		rdb.Close()
		if os.Getenv("CI") != "" {
			t.Fatalf("no Redis at %s: %v", redisAddr(), err)
		}
		t.Skipf("no Redis at %s: %v", redisAddr(), err)
	}

	l := &limiter{rdb: rdb, prefix: "rltest-" + strings.ToLower(rand.Text()[:8]) + ":"}
	t.Cleanup(func() {
		if keys, _ := rdb.Keys(ctx, l.prefix+"*").Result(); len(keys) > 0 {
			rdb.Del(ctx, keys...)
		}
		rdb.Close()
	})
	return l
}

// preset puts a count in place as if that many requests had come.
func preset(t *testing.T, l *limiter, lim rateLimit, key string, n int) {
	t.Helper()
	if err := l.rdb.Set(context.Background(), l.key(lim, key), n, lim.window).Err(); err != nil {
		t.Fatalf("presetting %s: %v", lim.name, err)
	}
}

// fill puts a key at its limit, so the next request is the first one over.
func fill(t *testing.T, l *limiter, lim rateLimit, key string) {
	t.Helper()
	preset(t, l, lim, key, int(lim.n))
}

func counted(t *testing.T, l *limiter, lim rateLimit, key string) int {
	t.Helper()
	n, err := l.rdb.Get(context.Background(), l.key(lim, key)).Int()
	if err != nil && err != redis.Nil {
		t.Fatalf("reading %s: %v", lim.name, err)
	}
	return n
}

func TestALimitLetsNThroughThenWaitsOutTheWindow(t *testing.T) {
	ctx := context.Background()
	l := testLimiter(t)
	lim := rateLimit{"three", 3, time.Second}

	for i := range 3 {
		if _, ok := l.hit(ctx, lim, "k"); !ok {
			t.Fatalf("hit %d of 3 refused", i+1)
		}
	}
	wait, ok := l.hit(ctx, lim, "k")
	if ok {
		t.Fatalf("the fourth hit went through")
	}
	if wait <= 0 || wait > time.Second {
		t.Fatalf("the refusal says wait %v, want within the 1 s window", wait)
	}
	if _, ok := l.hit(ctx, lim, "other"); !ok {
		t.Fatalf("another key was refused with the first one")
	}

	time.Sleep(wait + 50*time.Millisecond)
	if _, ok := l.hit(ctx, lim, "k"); !ok {
		t.Fatalf("still refused after the window ended")
	}
}

// The window starts at the first hit and stays put. If later hits pushed it
// back, a client hitting steadily would never be let through again.
func TestTheWindowDoesNotMoveWithLaterHits(t *testing.T) {
	ctx := context.Background()
	l := testLimiter(t)
	lim := rateLimit{"two", 2, time.Second}

	l.hit(ctx, lim, "k")
	time.Sleep(600 * time.Millisecond)
	l.hit(ctx, lim, "k")
	wait, ok := l.hit(ctx, lim, "k")
	if ok {
		t.Fatalf("the third hit went through")
	}
	if wait > 500*time.Millisecond {
		t.Fatalf("wait %v: the window moved with the later hits", wait)
	}
}

// Two nodes are two limiters with clients of their own; the count is one.
func TestTwoNodesShareOneCount(t *testing.T) {
	ctx := context.Background()
	a := testLimiter(t)
	b := &limiter{rdb: redis.NewClient(redisOptions()), prefix: a.prefix}
	t.Cleanup(func() { b.Close() })
	lim := rateLimit{"shared", 4, time.Minute}

	for i, node := range []*limiter{a, b, a, b} {
		if _, ok := node.hit(ctx, lim, "k"); !ok {
			t.Fatalf("hit %d refused", i+1)
		}
	}
	if _, ok := b.hit(ctx, lim, "k"); ok {
		t.Fatalf("the fifth hit went through: each node counted for itself")
	}
}

func TestCheckReadsWithoutCounting(t *testing.T) {
	ctx := context.Background()
	l := testLimiter(t)
	lim := rateLimit{"failures", 2, time.Minute}

	for range 5 {
		if _, ok := l.check(ctx, lim, "k"); !ok {
			t.Fatalf("check refused a key nothing was counted on")
		}
	}
	l.hit(ctx, lim, "k")
	l.hit(ctx, lim, "k")
	wait, ok := l.check(ctx, lim, "k")
	if ok || wait <= 0 {
		t.Fatalf("check after the limit was reached: ok=%v wait=%v", ok, wait)
	}
	l.clear(ctx, lim, "k")
	if _, ok := l.check(ctx, lim, "k"); !ok {
		t.Fatalf("still refused after clear")
	}
}

// With Redis gone the limits let requests through, and quickly: a Redis that
// accepts the connection and never answers is the slow case.
func TestWithoutRedisTheLimitsStepAsideQuickly(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()

	ctx := context.Background()
	lim := rateLimit{"any", 1, time.Minute}
	for _, addr := range []string{ln.Addr().String(), "127.0.0.1:1"} {
		l := &limiter{rdb: redis.NewClient(limiterOptions(&redis.Options{Addr: addr})), prefix: "rl:"}
		start := time.Now()
		_, hitOK := l.hit(ctx, lim, "k")
		_, checkOK := l.check(ctx, lim, "k")
		took := time.Since(start)
		l.Close()
		if !hitOK || !checkOK {
			t.Fatalf("%s: a Redis that does not answer refused the request", addr)
		}
		if took > 2*time.Second {
			t.Fatalf("%s: stepping aside took %v", addr, took)
		}
	}
}

func TestClientIPTrustsOnlyTheLastForwardedEntry(t *testing.T) {
	for _, tc := range []struct {
		name  string
		trust bool
		xff   []string
		want  string
	}{
		{"no proxy, header ignored", false, []string{"203.0.113.9"}, "192.0.2.1"},
		{"proxy, one entry", true, []string{"203.0.113.9"}, "203.0.113.9"},
		{"proxy, a spoofed entry before the real one", true, []string{"10.0.0.1, 203.0.113.9"}, "203.0.113.9"},
		{"proxy, two headers", true, []string{"10.0.0.1", "198.51.100.4, 203.0.113.9"}, "203.0.113.9"},
		{"proxy, no header", true, nil, "192.0.2.1"},
	} {
		r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		r.RemoteAddr = "192.0.2.1:40000"
		for _, v := range tc.xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		l := &limiter{trustProxy: tc.trust}
		if got := l.clientIP(r); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestTooManySaysWhenToComeBack(t *testing.T) {
	for _, tc := range []struct {
		wait time.Duration
		want string
	}{
		{1200 * time.Millisecond, "2"},
		{0, "1"},
		{59 * time.Minute, "3540"},
	} {
		w := httptest.NewRecorder()
		writeTooMany(w, tc.wait)
		var body struct {
			RetryAfter int `json:"retry_after"`
		}
		json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != tc.want ||
			body.RetryAfter == 0 {
			t.Errorf("wait %v: got %d, Retry-After %q, body %s", tc.wait, w.Code, w.Header().Get("Retry-After"), w.Body)
		}
	}
}

// loginFixture is an auth with one account, alice, and the limiter it counts in.
type loginFixture struct {
	a *auth
	l *limiter
}

const alicePassword = "correct horse battery"

func newLoginFixture(t *testing.T) loginFixture {
	t.Helper()
	l := testLimiter(t)
	db := testDB(t)
	users, sessions := newUserStore(db), newSessionStore(db)
	hash, err := hashPassword(alicePassword, defaultArgonParams)
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}
	if err := users.Create(context.Background(), &User{Username: "alice", PasswordHash: hash}); err != nil {
		t.Fatalf("creating alice: %v", err)
	}
	a, err := newAuth(users, sessions, l)
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	return loginFixture{a: a, l: l}
}

func (f loginFixture) login(ip, name, password string) int {
	return authCall(f.a.handleLogin, ip, name, password)
}

func authCall(h http.HandlerFunc, ip, name, password string) int {
	body, _ := json.Marshal(authRequest{Username: name, Password: password})
	r := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	r.RemoteAddr = ip + ":40000"
	w := httptest.NewRecorder()
	h(w, r)
	return w.Code
}

// The refusal has to come before argon2. With the hashing slots taken, a
// handler that got as far as the hash would answer 503 after its wait.
func TestLoginRefusesAnAddressBeforeTheHash(t *testing.T) {
	f := newLoginFixture(t)
	fill(t, f.l, limitLoginIP, "198.51.100.7")
	f.a.sem = make(chan struct{})

	if code := f.login("198.51.100.7", "alice", alicePassword); code != http.StatusTooManyRequests {
		t.Fatalf("an address over its limit got %d, want 429", code)
	}
}

func TestAFailedLoginCountsForTheAddressAndTheAccount(t *testing.T) {
	f := newLoginFixture(t)

	if code := f.login("198.51.100.7", "Alice", "wrong password"); code != http.StatusUnauthorized {
		t.Fatalf("a wrong password got %d, want 401", code)
	}
	// The name is counted as stored, so the case it was typed in is no way around.
	for _, c := range []struct {
		lim rateLimit
		key string
	}{
		{limitLoginIP, "198.51.100.7"},
		{limitLoginPair, "alice@198.51.100.7"},
		{limitLoginUser, "alice"},
	} {
		if n := counted(t, f.l, c.lim, c.key); n != 1 {
			t.Fatalf("%s counted %d, want 1", c.lim.name, n)
		}
	}
}

// Someone guessing from one address locks only that address out: the owner
// still gets in from their own.
func TestAGuessFromOneAddressLeavesTheOwnerIn(t *testing.T) {
	f := newLoginFixture(t)
	fill(t, f.l, limitLoginPair, "alice@203.0.113.66")

	if code := f.login("203.0.113.66", "alice", alicePassword); code != http.StatusTooManyRequests {
		t.Fatalf("the guessing address got %d, want 429", code)
	}
	if code := f.login("198.51.100.7", "alice", alicePassword); code != http.StatusOK {
		t.Fatalf("the owner's address got %d, want 200", code)
	}
}

func TestTheAccountCeilingHoldsEveryAddress(t *testing.T) {
	f := newLoginFixture(t)
	fill(t, f.l, limitLoginUser, "alice")

	if code := f.login("192.0.2.200", "alice", alicePassword); code != http.StatusTooManyRequests {
		t.Fatalf("a fresh address got %d with the account at its ceiling, want 429", code)
	}
}

func TestASuccessfulLoginForgivesItsAddressOnly(t *testing.T) {
	f := newLoginFixture(t)
	preset(t, f.l, limitLoginPair, "alice@198.51.100.7", 5)
	preset(t, f.l, limitLoginUser, "alice", 5)

	if code := f.login("198.51.100.7", "alice", alicePassword); code != http.StatusOK {
		t.Fatalf("login got %d, want 200", code)
	}
	if n := counted(t, f.l, limitLoginPair, "alice@198.51.100.7"); n != 0 {
		t.Fatalf("the address still has %d failures after the owner got in", n)
	}
	if n := counted(t, f.l, limitLoginUser, "alice"); n != 5 {
		t.Fatalf("the account count is %d, want the 5 it had", n)
	}
}

func TestANameThatCannotExistIsNotCounted(t *testing.T) {
	f := newLoginFixture(t)
	name := "no such name " + strings.Repeat("x", 100)

	if code := f.login("198.51.100.7", name, "anything"); code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", code)
	}
	if keys, _ := f.l.rdb.Keys(context.Background(), f.l.prefix+"login-*").Result(); len(keys) != 1 {
		t.Fatalf("keys after a login as an impossible name: %v, want the address only", keys)
	}
}

func TestRegistrationStopsAtTheAddressLimit(t *testing.T) {
	f := newLoginFixture(t)
	fill(t, f.l, limitRegisterIP, "198.51.100.7")

	if code := authCall(f.a.handleRegister, "198.51.100.7", "newcomer", alicePassword); code != http.StatusTooManyRequests {
		t.Fatalf("got %d, want 429", code)
	}
	if _, err := f.a.users.GetByUsername(context.Background(), "newcomer"); err == nil {
		t.Fatalf("the refused registration created the account")
	}
	if code := authCall(f.a.handleRegister, "192.0.2.200", "newcomer", alicePassword); code != http.StatusCreated {
		t.Fatalf("another address got %d, want 201", code)
	}
}

func TestSendingStopsAtTheLimitAndStoresNothing(t *testing.T) {
	db := testDB(t)
	channels, messages := newMessageTestStores(t, db)
	l := testLimiter(t)
	owner := person("owner")
	ch := createChannel(t, channels, "room", owner)
	h := NewHub()
	s := &server{channels: channels, messages: messages, hub: h, bus: h, limits: l}
	fill(t, l, limitMessages, owner.UserID.Hex())

	send := map[string]string{"channel_id": ch.ID.Hex(), "text": "one too many", "client_msg_id": "c1"}
	if code, body := callMessageHandler(t, s.handleSendMessage, "", send, owner); code != http.StatusTooManyRequests {
		t.Fatalf("send got %d %s, want 429", code, body)
	}
	if code, body := callMessageHandler(t, s.handleForwardMessage, bson.NewObjectID().Hex(),
		map[string]string{"channel_id": ch.ID.Hex()}, owner); code != http.StatusTooManyRequests {
		t.Fatalf("forward got %d %s, want 429", code, body)
	}
	if n, _ := messages.col.CountDocuments(context.Background(), bson.M{"channel_id": ch.ID}); n != 0 {
		t.Fatalf("%d messages stored past the limit", n)
	}
	// Someone else's allowance is their own.
	other := person("other")
	if err := channels.AddMember(context.Background(), ch.ID, other); err != nil {
		t.Fatalf("joining: %v", err)
	}
	if code, body := callMessageHandler(t, s.handleSendMessage, "", send, other); code != http.StatusCreated {
		t.Fatalf("another member got %d %s, want 201", code, body)
	}
}

func TestUploadsStopAtTheLimitAndStoreNothing(t *testing.T) {
	db := testDB(t)
	l := testLimiter(t)
	media := newMediaStore(db)
	s := &server{media: media, limits: l}
	alice := person("alice")
	fill(t, l, limitUploads, alice.UserID.Hex())

	if code, body := uploadMedia(s, testPNG(t, 2, 2), alice); code != http.StatusTooManyRequests {
		t.Fatalf("picture got %d %s, want 429", code, body)
	}
	if code, body := setAvatar(s, testPNG(t, 2, 2), alice); code != http.StatusTooManyRequests {
		t.Fatalf("avatar got %d %s, want 429", code, body)
	}
	if n, _ := media.col.CountDocuments(context.Background(), bson.M{}); n != 0 {
		t.Fatalf("%d media records stored past the limit", n)
	}
}
