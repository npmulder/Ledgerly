package identity

import (
	"net"
	nethttp "net/http"
	"strings"
	"sync"
	"time"
)

type LoginRateLimit struct {
	Capacity    int
	RefillEvery time.Duration
}

func DefaultLoginRateLimit() LoginRateLimit {
	return LoginRateLimit{
		Capacity:    5,
		RefillEvery: time.Minute,
	}
}

type LoginRateLimiter struct {
	mu      sync.Mutex
	limit   LoginRateLimit
	buckets map[string]*loginBucket
	now     func() time.Time
}

type loginBucket struct {
	tokens int
	last   time.Time
}

func NewLoginRateLimiter(limit LoginRateLimit) *LoginRateLimiter {
	if limit.Capacity <= 0 {
		limit.Capacity = DefaultLoginRateLimit().Capacity
	}
	if limit.RefillEvery <= 0 {
		limit.RefillEvery = DefaultLoginRateLimit().RefillEvery
	}
	return &LoginRateLimiter{
		limit:   limit,
		buckets: make(map[string]*loginBucket),
		now:     time.Now,
	}
}

func (l *LoginRateLimiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now().UTC()
	bucket, ok := l.buckets[ip]
	if !ok {
		l.buckets[ip] = &loginBucket{
			tokens: l.limit.Capacity - 1,
			last:   now,
		}
		return true
	}

	if refill := int(now.Sub(bucket.last) / l.limit.RefillEvery); refill > 0 {
		bucket.tokens += refill
		if bucket.tokens > l.limit.Capacity {
			bucket.tokens = l.limit.Capacity
		}
		bucket.last = bucket.last.Add(time.Duration(refill) * l.limit.RefillEvery)
	}

	if bucket.tokens <= 0 {
		return false
	}
	bucket.tokens--
	return true
}

func clientIP(r *nethttp.Request) string {
	if forwardedFor := r.Header.Get("X-Forwarded-For"); forwardedFor != "" {
		ip, _, _ := strings.Cut(forwardedFor, ",")
		ip = strings.TrimSpace(ip)
		if ip != "" {
			return ip
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if strings.TrimSpace(r.RemoteAddr) != "" {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return "unknown"
}
