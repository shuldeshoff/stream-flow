package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

type RedisCache struct {
	client *redis.Client
}

func NewRedisCache(addr, password string, db int) (*RedisCache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     10,
	})

	// Проверяем соединение
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	log.Info().Str("address", addr).Msg("Connected to Redis")

	return &RedisCache{
		client: client,
	}, nil
}

func (r *RedisCache) Close() error {
	return r.client.Close()
}

// Get получает значение из кэша
func (r *RedisCache) Get(ctx context.Context, key string) (string, error) {
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", fmt.Errorf("key not found")
	}
	if err != nil {
		return "", err
	}
	return val, nil
}

// Set сохраняет значение в кэш с TTL
func (r *RedisCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	return r.client.Set(ctx, key, value, ttl).Err()
}

// SetJSON сохраняет JSON в кэш
func (r *RedisCache) SetJSON(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	jsonData, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}
	return r.Set(ctx, key, jsonData, ttl)
}

// GetJSON получает JSON из кэша
func (r *RedisCache) GetJSON(ctx context.Context, key string, dest interface{}) error {
	val, err := r.Get(ctx, key)
	if err != nil {
		return err
	}

	if err := json.Unmarshal([]byte(val), dest); err != nil {
		return fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	return nil
}

// Increment увеличивает счетчик
func (r *RedisCache) Increment(ctx context.Context, key string) (int64, error) {
	return r.client.Incr(ctx, key).Result()
}

// IncrementBy увеличивает счетчик на указанное значение
func (r *RedisCache) IncrementBy(ctx context.Context, key string, value int64) (int64, error) {
	return r.client.IncrBy(ctx, key, value).Result()
}

// Delete удаляет ключ из кэша
func (r *RedisCache) Delete(ctx context.Context, keys ...string) error {
	return r.client.Del(ctx, keys...).Err()
}

// Expire устанавливает TTL для ключа
func (r *RedisCache) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return r.client.Expire(ctx, key, ttl).Err()
}

// GetEventTypeStats получает статистику по типу события (агрегация за временное окно)
func (r *RedisCache) GetEventTypeStats(ctx context.Context, eventType string, window time.Duration) (int64, error) {
	key := fmt.Sprintf("stats:type:%s:%s", eventType, getCurrentWindow(window))
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	var count int64
	if _, err := fmt.Sscanf(val, "%d", &count); err != nil {
		return 0, err
	}

	return count, nil
}

// IncrementEventTypeStats увеличивает счетчик для типа события
func (r *RedisCache) IncrementEventTypeStats(ctx context.Context, eventType string, window time.Duration) error {
	key := fmt.Sprintf("stats:type:%s:%s", eventType, getCurrentWindow(window))
	_, err := r.client.Incr(ctx, key).Result()
	if err != nil {
		return err
	}

	// Устанавливаем TTL на 2x window чтобы не потерять данные
	return r.client.Expire(ctx, key, window*2).Err()
}

// GetSourceStats получает статистику по источнику
func (r *RedisCache) GetSourceStats(ctx context.Context, source string, window time.Duration) (int64, error) {
	key := fmt.Sprintf("stats:source:%s:%s", source, getCurrentWindow(window))
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	var count int64
	if _, err := fmt.Sscanf(val, "%d", &count); err != nil {
		return 0, err
	}

	return count, nil
}

// IncrementSourceStats увеличивает счетчик для источника
func (r *RedisCache) IncrementSourceStats(ctx context.Context, source string, window time.Duration) error {
	key := fmt.Sprintf("stats:source:%s:%s", source, getCurrentWindow(window))
	_, err := r.client.Incr(ctx, key).Result()
	if err != nil {
		return err
	}

	return r.client.Expire(ctx, key, window*2).Err()
}

// getCurrentWindow возвращает текущее временное окно
func getCurrentWindow(window time.Duration) string {
	now := time.Now()
	windowStart := now.Truncate(window)
	return windowStart.Format("2006-01-02T15:04:05")
}

// GetAllEventTypeStats получает статистику по всем типам событий
func (r *RedisCache) GetAllEventTypeStats(ctx context.Context, window time.Duration) (map[string]int64, error) {
	pattern := fmt.Sprintf("stats:type:*:%s", getCurrentWindow(window))
	keys, err := r.client.Keys(ctx, pattern).Result()
	if err != nil {
		return nil, err
	}

	stats := make(map[string]int64)
	for _, key := range keys {
		val, err := r.client.Get(ctx, key).Result()
		if err != nil {
			continue
		}

		var count int64
		if _, err := fmt.Sscanf(val, "%d", &count); err != nil {
			continue
		}

		// Извлекаем тип события из ключа
		// stats:type:EVENT_TYPE:TIMESTAMP
		parts := len("stats:type:")
		endIdx := len(key) - len(getCurrentWindow(window)) - 1
		if endIdx > parts {
			eventType := key[parts:endIdx]
			stats[eventType] = count
		}
	}

	return stats, nil
}

