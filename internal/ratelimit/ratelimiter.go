package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/shuldeshoff/stream-flow/internal/cache"
	"golang.org/x/time/rate"
)

// RateLimiter управляет rate limiting для клиентов
type RateLimiter struct {
	cache     *cache.RedisCache
	limiters  map[string]*rate.Limiter
	mu        sync.RWMutex
	rps       int           // requests per second
	burst     int           // burst size
	ttl       time.Duration // TTL для неактивных лимитеров
	cleanupCh chan struct{}
}

// NewRateLimiter создает новый rate limiter
func NewRateLimiter(cache *cache.RedisCache, rps, burst int) *RateLimiter {
	rl := &RateLimiter{
		cache:     cache,
		limiters:  make(map[string]*rate.Limiter),
		rps:       rps,
		burst:     burst,
		ttl:       5 * time.Minute,
		cleanupCh: make(chan struct{}),
	}

	// Запускаем очистку неактивных лимитеров
	go rl.cleanup()

	return rl
}

// Allow проверяет можно ли пропустить запрос от клиента
func (rl *RateLimiter) Allow(clientID string) bool {
	// Если Redis доступен, используем распределенный rate limiting
	if rl.cache != nil {
		return rl.allowDistributed(clientID)
	}

	// Fallback на локальный rate limiting
	return rl.allowLocal(clientID)
}

// allowLocal использует локальный in-memory rate limiting
func (rl *RateLimiter) allowLocal(clientID string) bool {
	rl.mu.Lock()
	limiter, exists := rl.limiters[clientID]
	if !exists {
		limiter = rate.NewLimiter(rate.Limit(rl.rps), rl.burst)
		rl.limiters[clientID] = limiter
	}
	rl.mu.Unlock()

	return limiter.Allow()
}

// allowDistributed использует Redis для распределенного rate limiting
func (rl *RateLimiter) allowDistributed(clientID string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Используем sliding window algorithm с Redis
	key := fmt.Sprintf("ratelimit:%s", clientID)
	
	// Инкрементируем счетчик
	count, err := rl.cache.Increment(ctx, key)
	if err != nil {
		log.Warn().Err(err).Str("client", clientID).Msg("Redis rate limit check failed, falling back to local")
		return rl.allowLocal(clientID)
	}

	// Устанавливаем TTL на первом запросе
	if count == 1 {
		rl.cache.Expire(ctx, key, 1*time.Second)
	}

	// Проверяем лимит
	allowed := count <= int64(rl.rps)
	
	if !allowed {
		log.Debug().
			Str("client", clientID).
			Int64("count", count).
			Int("limit", rl.rps).
			Msg("Rate limit exceeded")
	}

	return allowed
}

// AllowN проверяет можно ли пропустить N запросов
func (rl *RateLimiter) AllowN(clientID string, n int) bool {
	if rl.cache != nil {
		return rl.allowNDistributed(clientID, n)
	}

	rl.mu.RLock()
	limiter, exists := rl.limiters[clientID]
	rl.mu.RUnlock()

	if !exists {
		rl.mu.Lock()
		limiter = rate.NewLimiter(rate.Limit(rl.rps), rl.burst)
		rl.limiters[clientID] = limiter
		rl.mu.Unlock()
	}

	return limiter.AllowN(time.Now(), n)
}

func (rl *RateLimiter) allowNDistributed(clientID string, n int) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	key := fmt.Sprintf("ratelimit:%s", clientID)
	
	count, err := rl.cache.IncrementBy(ctx, key, int64(n))
	if err != nil {
		log.Warn().Err(err).Msg("Redis rate limit failed")
		return rl.AllowN(clientID, n)
	}

	if count == int64(n) {
		rl.cache.Expire(ctx, key, 1*time.Second)
	}

	return count <= int64(rl.rps)
}

// GetLimit возвращает текущий лимит для клиента
func (rl *RateLimiter) GetLimit(clientID string) (limit, remaining int) {
	if rl.cache != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		key := fmt.Sprintf("ratelimit:%s", clientID)
		val, err := rl.cache.Get(ctx, key)
		if err != nil {
			return rl.rps, rl.rps
		}

		var current int64
		fmt.Sscanf(val, "%d", &current)
		
		remaining := rl.rps - int(current)
		if remaining < 0 {
			remaining = 0
		}

		return rl.rps, remaining
	}

	// Локальный подсчет
	rl.mu.RLock()
	limiter, exists := rl.limiters[clientID]
	rl.mu.RUnlock()

	if !exists {
		return rl.rps, rl.rps
	}

	tokens := int(limiter.Tokens())
	if tokens > rl.rps {
		tokens = rl.rps
	}

	return rl.rps, tokens
}

// ResetClient сбрасывает лимит для клиента
func (rl *RateLimiter) ResetClient(clientID string) {
	if rl.cache != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		key := fmt.Sprintf("ratelimit:%s", clientID)
		rl.cache.Delete(ctx, key)
	}

	rl.mu.Lock()
	delete(rl.limiters, clientID)
	rl.mu.Unlock()
}

// cleanup периодически очищает неактивные лимитеры
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			// В реальной реализации нужно отслеживать последнее использование
			// Для простоты очищаем все раз в минуту
			if len(rl.limiters) > 10000 {
				rl.limiters = make(map[string]*rate.Limiter)
				log.Info().Msg("Cleaned up rate limiters")
			}
			rl.mu.Unlock()

		case <-rl.cleanupCh:
			return
		}
	}
}

// Stop останавливает rate limiter
func (rl *RateLimiter) Stop() {
	close(rl.cleanupCh)
}

// Stats возвращает статистику rate limiter
func (rl *RateLimiter) Stats() map[string]interface{} {
	rl.mu.RLock()
	activeClients := len(rl.limiters)
	rl.mu.RUnlock()

	return map[string]interface{}{
		"active_clients": activeClients,
		"rps_limit":      rl.rps,
		"burst_size":     rl.burst,
		"backend":        rl.getBackend(),
	}
}

func (rl *RateLimiter) getBackend() string {
	if rl.cache != nil {
		return "redis"
	}
	return "memory"
}

