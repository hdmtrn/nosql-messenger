package main

import (
	"context"
	"errors"
	"log"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// rateLimit allows n hits per window for one key. The window is fixed: it
// starts at the first hit and does not move, so a key cannot be kept open by
// hitting it steadily.
type rateLimit struct {
	name   string
	n      int64
	window time.Duration
}

var (
	// Every attempt from one address: one password tried across many accounts.
	limitLoginIP = rateLimit{"login-ip", 100, time.Minute}
	// Failures for one account from one address. A stranger elsewhere runs
	// into a count of their own, not the owner's.
	limitLoginPair = rateLimit{"login-pair", 10, 15 * time.Minute}
	// Failures for one account from everywhere: the ceiling for a guess spread
	// over many addresses.
	limitLoginUser  = rateLimit{"login-user", 100, time.Hour}
	limitRegisterIP = rateLimit{"register-ip", 50, time.Hour}
	limitMessages   = rateLimit{"messages", 30, 10 * time.Second}
	limitUploads    = rateLimit{"uploads", 60, time.Minute}
)

// limiter keeps the counts in Redis, so that the nodes share them: a count
// kept in each process would give every node an allowance of its own.
//
// A nil limiter allows everything, which is what handler tests that are not
// about limits run with.
type limiter struct {
	rdb    *redis.Client
	prefix string

	// trustProxy takes the client's address from X-Forwarded-For. Only behind
	// the ALB, which writes it; anywhere else the client writes it.
	trustProxy bool

	lastLog atomic.Int64
}

func newLimiter(trustProxy bool) *limiter {
	return &limiter{
		rdb:        redis.NewClient(limiterOptions(redisOptions())),
		prefix:     "rl:",
		trustProxy: trustProxy,
	}
}

// limiterOptions gives the limiter a client of its own that gives up fast.
// With Redis unreachable the defaults would hold every request for a 5 s dial
// and three retries before the limits stepped aside.
func limiterOptions(o *redis.Options) *redis.Options {
	o.DialTimeout = 250 * time.Millisecond
	o.ReadTimeout = 250 * time.Millisecond
	o.WriteTimeout = 250 * time.Millisecond
	o.MaxRetries = -1
	return o
}

func (l *limiter) Close() error {
	return l.rdb.Close()
}

func (l *limiter) key(lim rateLimit, key string) string {
	return l.prefix + lim.name + ":" + key
}

// hit counts one request and says whether it is within the limit, and if not,
// how long until the window ends. INCR is atomic, so two nodes counting one key
// each get a number of their own and no script is needed. EXPIRE NX starts the
// window on the first hit and leaves it alone after; MULTI makes sure a key is
// never left without one, which would close it for good.
func (l *limiter) hit(ctx context.Context, lim rateLimit, key string) (time.Duration, bool) {
	if l == nil {
		return 0, true
	}
	k := l.key(lim, key)
	var count *redis.IntCmd
	var ttl *redis.DurationCmd
	_, err := l.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		count = p.Incr(ctx, k)
		p.ExpireNX(ctx, k, lim.window)
		ttl = p.PTTL(ctx, k)
		return nil
	})
	if err != nil {
		l.stepAside(err)
		return 0, true
	}
	if count.Val() <= lim.n {
		return 0, true
	}
	return ttl.Val(), false
}

// check is hit without the count, for the login limits that only failures
// move: they are read before the password is, and counted after it fails.
func (l *limiter) check(ctx context.Context, lim rateLimit, key string) (time.Duration, bool) {
	if l == nil {
		return 0, true
	}
	k := l.key(lim, key)
	var count *redis.StringCmd
	var ttl *redis.DurationCmd
	_, err := l.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		count = p.Get(ctx, k)
		ttl = p.PTTL(ctx, k)
		return nil
	})
	if errors.Is(err, redis.Nil) {
		return 0, true
	}
	if err != nil {
		l.stepAside(err)
		return 0, true
	}
	if n, _ := count.Int64(); n < lim.n {
		return 0, true
	}
	return ttl.Val(), false
}

func (l *limiter) clear(ctx context.Context, lim rateLimit, key string) {
	if l == nil {
		return
	}
	if err := l.rdb.Del(ctx, l.key(lim, key)).Err(); err != nil {
		l.stepAside(err)
	}
}

// allow is hit for a handler: over the limit it answers 429 itself.
func (l *limiter) allow(w http.ResponseWriter, r *http.Request, lim rateLimit, key string) bool {
	wait, ok := l.hit(r.Context(), lim, key)
	if !ok {
		writeTooMany(w, wait)
	}
	return ok
}

// stepAside lets requests through while Redis is away, and says so at most
// once a minute: refusing everyone because a counter is unreachable would turn
// a Redis outage into a full one.
func (l *limiter) stepAside(err error) {
	now := time.Now().Unix()
	last := l.lastLog.Load()
	if now-last < 60 || !l.lastLog.CompareAndSwap(last, now) {
		return
	}
	log.Printf("rate limits step aside, Redis does not answer: %v", err)
}

// clientIP is the address limits are counted by. Behind the ALB it is the last
// X-Forwarded-For entry: the ALB appends the address that connected to it, and
// everything before that came from the client.
func (l *limiter) clientIP(r *http.Request) string {
	if l != nil && l.trustProxy {
		if values := r.Header.Values("X-Forwarded-For"); len(values) > 0 {
			entries := strings.Split(values[len(values)-1], ",")
			if ip := strings.TrimSpace(entries[len(entries)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeTooMany(w http.ResponseWriter, wait time.Duration) {
	secs := max(1, int(math.Ceil(wait.Seconds())))
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeJSON(w, http.StatusTooManyRequests, map[string]any{
		"error":       "too many requests",
		"retry_after": secs,
	})
}
