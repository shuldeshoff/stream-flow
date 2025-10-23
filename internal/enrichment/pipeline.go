package enrichment

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sul/streamflow/internal/cache"
	"github.com/sul/streamflow/internal/models"
)

// Enricher интерфейс для обогащения событий
type Enricher interface {
	Enrich(ctx context.Context, event *models.Event) error
	Name() string
}

// Pipeline управляет цепочкой enrichers
type Pipeline struct {
	enrichers []Enricher
	cache     *cache.RedisCache
	mu        sync.RWMutex
	stats     PipelineStats
}

type PipelineStats struct {
	TotalProcessed int64
	TotalEnriched  int64
	TotalErrors    int64
	mu             sync.RWMutex
}

func NewPipeline(cache *cache.RedisCache) *Pipeline {
	return &Pipeline{
		enrichers: make([]Enricher, 0),
		cache:     cache,
	}
}

// AddEnricher добавляет enricher в pipeline
func (p *Pipeline) AddEnricher(e Enricher) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.enrichers = append(p.enrichers, e)
	log.Info().Str("enricher", e.Name()).Msg("Enricher added to pipeline")
}

// Process обрабатывает событие через все enrichers
func (p *Pipeline) Process(ctx context.Context, event *models.Event) error {
	p.mu.RLock()
	enrichers := p.enrichers
	p.mu.RUnlock()

	p.stats.mu.Lock()
	p.stats.TotalProcessed++
	p.stats.mu.Unlock()

	for _, enricher := range enrichers {
		if err := enricher.Enrich(ctx, event); err != nil {
			p.stats.mu.Lock()
			p.stats.TotalErrors++
			p.stats.mu.Unlock()

			log.Warn().
				Err(err).
				Str("enricher", enricher.Name()).
				Str("event_id", event.ID).
				Msg("Enrichment failed")
			
			// Продолжаем даже если один enricher failed
			continue
		}
	}

	p.stats.mu.Lock()
	p.stats.TotalEnriched++
	p.stats.mu.Unlock()

	return nil
}

// GetStats возвращает статистику pipeline
func (p *Pipeline) GetStats() PipelineStats {
	p.stats.mu.RLock()
	defer p.stats.mu.RUnlock()
	
	return PipelineStats{
		TotalProcessed: p.stats.TotalProcessed,
		TotalEnriched:  p.stats.TotalEnriched,
		TotalErrors:    p.stats.TotalErrors,
	}
}

// --- Built-in Enrichers ---

// TimestampEnricher добавляет серверные timestamps
type TimestampEnricher struct{}

func (e *TimestampEnricher) Name() string {
	return "timestamp"
}

func (e *TimestampEnricher) Enrich(ctx context.Context, event *models.Event) error {
	if event.Metadata == nil {
		event.Metadata = make(map[string]string)
	}
	
	event.Metadata["server_timestamp"] = time.Now().Format(time.RFC3339)
	event.Metadata["enriched_at"] = time.Now().Format(time.RFC3339)
	
	return nil
}

// GeoIPEnricher добавляет геолокацию по IP
type GeoIPEnricher struct {
	cache *cache.RedisCache
}

func NewGeoIPEnricher(cache *cache.RedisCache) *GeoIPEnricher {
	return &GeoIPEnricher{cache: cache}
}

func (e *GeoIPEnricher) Name() string {
	return "geoip"
}

func (e *GeoIPEnricher) Enrich(ctx context.Context, event *models.Event) error {
	// Ищем IP в данных события
	ip, ok := event.Data["ip"].(string)
	if !ok || ip == "" {
		return nil // нет IP, пропускаем
	}

	// Проверяем кэш
	if e.cache != nil {
		cacheKey := fmt.Sprintf("geoip:%s", ip)
		if cachedGeo, err := e.cache.Get(ctx, cacheKey); err == nil {
			if event.Metadata == nil {
				event.Metadata = make(map[string]string)
			}
			event.Metadata["geo"] = cachedGeo
			return nil
		}
	}

	// В production здесь был бы реальный GeoIP lookup
	// Для демо добавляем заглушку
	geo := e.mockGeoLookup(ip)
	
	if event.Metadata == nil {
		event.Metadata = make(map[string]string)
	}
	event.Metadata["geo"] = geo

	// Кэшируем результат
	if e.cache != nil {
		cacheKey := fmt.Sprintf("geoip:%s", ip)
		e.cache.Set(ctx, cacheKey, geo, 24*time.Hour)
	}

	return nil
}

func (e *GeoIPEnricher) mockGeoLookup(ip string) string {
	// Mock данные
	return fmt.Sprintf(`{"ip":"%s","country":"RU","city":"Moscow"}`, ip)
}

// UserAgentEnricher парсит user agent
type UserAgentEnricher struct{}

func (e *UserAgentEnricher) Name() string {
	return "user_agent"
}

func (e *UserAgentEnricher) Enrich(ctx context.Context, event *models.Event) error {
	ua, ok := event.Data["user_agent"].(string)
	if !ok || ua == "" {
		return nil
	}

	// В production здесь был бы парсинг с помощью библиотеки
	// Для демо добавляем простую логику
	parsed := e.mockParseUA(ua)
	
	if event.Metadata == nil {
		event.Metadata = make(map[string]string)
	}
	event.Metadata["browser"] = parsed["browser"]
	event.Metadata["device"] = parsed["device"]
	event.Metadata["os"] = parsed["os"]

	return nil
}

func (e *UserAgentEnricher) mockParseUA(ua string) map[string]string {
	// Mock парсинг
	return map[string]string{
		"browser": "Chrome",
		"device":  "Desktop",
		"os":      "MacOS",
	}
}

// SessionEnricher добавляет session ID
type SessionEnricher struct {
	cache *cache.RedisCache
}

func NewSessionEnricher(cache *cache.RedisCache) *SessionEnricher {
	return &SessionEnricher{cache: cache}
}

func (e *SessionEnricher) Name() string {
	return "session"
}

func (e *SessionEnricher) Enrich(ctx context.Context, event *models.Event) error {
	userID, ok := event.Data["user_id"].(string)
	if !ok || userID == "" {
		return nil
	}

	// Получаем или создаем session ID
	sessionID := e.getOrCreateSession(ctx, userID)
	
	if event.Metadata == nil {
		event.Metadata = make(map[string]string)
	}
	event.Metadata["session_id"] = sessionID

	return nil
}

func (e *SessionEnricher) getOrCreateSession(ctx context.Context, userID string) string {
	if e.cache == nil {
		return fmt.Sprintf("session_%s_%d", userID, time.Now().Unix())
	}

	sessionKey := fmt.Sprintf("session:%s", userID)
	
	// Проверяем существующую сессию
	if sessionID, err := e.cache.Get(ctx, sessionKey); err == nil {
		return sessionID
	}

	// Создаем новую сессию
	sessionID := fmt.Sprintf("session_%s_%d", userID, time.Now().Unix())
	e.cache.Set(ctx, sessionKey, sessionID, 30*time.Minute)
	
	return sessionID
}

// CounterEnricher добавляет счетчики событий
type CounterEnricher struct {
	cache *cache.RedisCache
}

func NewCounterEnricher(cache *cache.RedisCache) *CounterEnricher {
	return &CounterEnricher{cache: cache}
}

func (e *CounterEnricher) Name() string {
	return "counter"
}

func (e *CounterEnricher) Enrich(ctx context.Context, event *models.Event) error {
	if e.cache == nil {
		return nil
	}

	// Счетчик по типу события
	typeKey := fmt.Sprintf("counter:type:%s", event.Type)
	count, _ := e.cache.Increment(ctx, typeKey)
	e.cache.Expire(ctx, typeKey, 1*time.Hour)

	// Счетчик по источнику
	sourceKey := fmt.Sprintf("counter:source:%s", event.Source)
	sourceCount, _ := e.cache.Increment(ctx, sourceKey)
	e.cache.Expire(ctx, sourceKey, 1*time.Hour)

	if event.Metadata == nil {
		event.Metadata = make(map[string]string)
	}
	event.Metadata["event_count"] = fmt.Sprintf("%d", count)
	event.Metadata["source_count"] = fmt.Sprintf("%d", sourceCount)

	return nil
}

