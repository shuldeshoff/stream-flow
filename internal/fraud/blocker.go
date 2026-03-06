package fraud

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/shuldeshoff/stream-flow/internal/cache"
)

// Blocker maintains a card-level blocklist backed by Redis (durable)
// and an in-memory map (fast reads).
type Blocker struct {
	mu        sync.RWMutex
	local     map[string]time.Time // cardNumber → blockedAt
	cache     *cache.RedisCache    // may be nil
	blockTTL  time.Duration
}

// NewBlocker creates a Blocker. cache may be nil (in-memory only fallback).
func NewBlocker(redisCache *cache.RedisCache, blockTTL time.Duration) *Blocker {
	if blockTTL == 0 {
		blockTTL = 24 * time.Hour
	}
	return &Blocker{
		local:    make(map[string]time.Time),
		cache:    redisCache,
		blockTTL: blockTTL,
	}
}

// Block marks a card as blocked in both local memory and Redis.
func (b *Blocker) Block(ctx context.Context, cardNumber, reason string) {
	b.mu.Lock()
	b.local[cardNumber] = time.Now()
	b.mu.Unlock()

	if b.cache != nil {
		key := fmt.Sprintf("blocked_card:%s", cardNumber)
		if err := b.cache.Set(ctx, key, reason, b.blockTTL); err != nil {
			log.Error().Err(err).Str("card", cardNumber).Msg("Failed to persist block to Redis")
		}
	}

	log.Warn().Str("card", cardNumber).Str("reason", reason).Msg("Card blocked")
}

// Unblock removes a card from the blocklist.
func (b *Blocker) Unblock(ctx context.Context, cardNumber string) {
	b.mu.Lock()
	delete(b.local, cardNumber)
	b.mu.Unlock()

	if b.cache != nil {
		key := fmt.Sprintf("blocked_card:%s", cardNumber)
		_ = b.cache.Delete(ctx, key)
	}

	log.Info().Str("card", cardNumber).Msg("Card unblocked")
}

// IsBlocked returns true if the card is currently blocked.
// Checks local map first (fast), then falls back to Redis.
func (b *Blocker) IsBlocked(cardNumber string) bool {
	b.mu.RLock()
	_, blocked := b.local[cardNumber]
	b.mu.RUnlock()
	if blocked {
		return true
	}

	if b.cache != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		key := fmt.Sprintf("blocked_card:%s", cardNumber)
		_, err := b.cache.Get(ctx, key)
		if err == nil {
			// Warm local cache to avoid repeated Redis lookups.
			b.mu.Lock()
			b.local[cardNumber] = time.Now()
			b.mu.Unlock()
			return true
		}
	}

	return false
}
