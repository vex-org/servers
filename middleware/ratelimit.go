package middleware

import (
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
)

type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	limit    int
	window   time.Duration
}

type visitor struct {
	count    int
	resetAt  time.Time
}

// RateLimit returns a Fiber middleware that limits requests per IP.
// limit: max requests, windowSec: time window in seconds.
func RateLimit(limit, windowSec int) fiber.Handler {
	rl := &rateLimiter{
		visitors: make(map[string]*visitor),
		limit:    limit,
		window:   time.Duration(windowSec) * time.Second,
	}

	// Cleanup stale entries every minute
	go func() {
		for {
			time.Sleep(time.Minute)
			rl.mu.Lock()
			now := time.Now()
			for ip, v := range rl.visitors {
				if now.After(v.resetAt) {
					delete(rl.visitors, ip)
				}
			}
			rl.mu.Unlock()
		}
	}()

	return func(c fiber.Ctx) error {
		ip := c.IP()
		rl.mu.Lock()
		v, exists := rl.visitors[ip]
		now := time.Now()

		if !exists || now.After(v.resetAt) {
			rl.visitors[ip] = &visitor{count: 1, resetAt: now.Add(rl.window)}
			rl.mu.Unlock()
			return c.Next()
		}

		if v.count >= rl.limit {
			rl.mu.Unlock()
			retryAfter := v.resetAt.Sub(now).Seconds()
			return c.Status(429).JSON(fiber.Map{
				"error":        "rate limit exceeded",
				"retry_after_s": retryAfter,
			})
		}

		v.count++
		rl.mu.Unlock()
		return c.Next()
	}
}
