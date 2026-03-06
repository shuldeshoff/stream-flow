package ingestion

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/shuldeshoff/stream-flow/internal/config"
	"github.com/shuldeshoff/stream-flow/internal/kafka"
	"github.com/shuldeshoff/stream-flow/internal/metrics"
	"github.com/shuldeshoff/stream-flow/internal/models"
	"github.com/shuldeshoff/stream-flow/internal/processor"
	"github.com/shuldeshoff/stream-flow/internal/ratelimit"
	"github.com/shuldeshoff/stream-flow/internal/security"
)

type HTTPServer struct {
	config      config.ServerConfig
	tlsConfig   *tls.Config
	jwtManager  *security.JWTManager
	processor   *processor.EventProcessor
	rateLimiter *ratelimit.RateLimiter
	server      *http.Server
	// kafkaProducer is optional; when non-nil events are published to Kafka
	// before the in-process fallback path.
	kafkaProducer *kafka.Producer
}

func NewHTTPServer(cfg config.ServerConfig, tlsCfg *tls.Config, jwtMgr *security.JWTManager, proc *processor.EventProcessor, rl *ratelimit.RateLimiter) *HTTPServer {
	return &HTTPServer{
		config:      cfg,
		tlsConfig:   tlsCfg,
		jwtManager:  jwtMgr,
		processor:   proc,
		rateLimiter: rl,
	}
}

// SetKafkaProducer attaches a Kafka producer so that accepted events are
// published to kafka.TopicEventsRaw in addition to the in-process pipeline.
// When Kafka is enabled, the in-process Submit call is skipped to avoid
// double-processing; it serves as fallback only when Kafka is unavailable.
func (s *HTTPServer) SetKafkaProducer(p *kafka.Producer) {
	s.kafkaProducer = p
}

func (s *HTTPServer) Start() error {
	mux := http.NewServeMux()
	
	// Основной endpoint для приема событий (без JWT для обратной совместимости)
	mux.HandleFunc("/api/v1/events", s.handleEvents)
	mux.HandleFunc("/api/v1/events/batch", s.handleBatchEvents)
	
	// Health check
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ready", s.handleReady)

	// Применяем JWT middleware если включен
	var handler http.Handler = mux
	if s.jwtManager != nil {
		// Используем optional middleware для обратной совместимости
		handler = s.jwtManager.OptionalHTTPMiddleware(mux)
		log.Info().Msg("JWT authentication enabled (optional mode)")
	}

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.config.Port),
		Handler:      handler,
		ReadTimeout:  time.Duration(s.config.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(s.config.WriteTimeout) * time.Second,
		TLSConfig:    s.tlsConfig,
	}

	// Запускаем с TLS или без
	if s.tlsConfig != nil {
		log.Info().Msgf("HTTPS server listening on :%d (TLS enabled)", s.config.Port)
		return s.server.ListenAndServeTLS("", "") // Сертификаты уже в TLSConfig
	}

	log.Warn().Msgf("HTTP server listening on :%d (TLS DISABLED - not recommended for production!)", s.config.Port)
	return s.server.ListenAndServe()
}

func (s *HTTPServer) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func (s *HTTPServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Rate limiting
	clientID := getClientID(r)
	if s.rateLimiter != nil && !s.rateLimiter.Allow(clientID) {
		metrics.IncEventsReceived("rate_limited")
		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", s.getRateLimitInfo(clientID)))
		w.Header().Set("Retry-After", "1")
		http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	startTime := time.Now()
	defer func() {
		metrics.RecordIngestionLatency(time.Since(startTime).Seconds())
	}()

	var event models.Event
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		metrics.IncEventsReceived("error")
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	// Валидация
	if event.ID == "" {
		metrics.IncEventsReceived("error")
		http.Error(w, "Event ID is required", http.StatusBadRequest)
		return
	}

	if event.Type == "" {
		metrics.IncEventsReceived("error")
		http.Error(w, "Event type is required", http.StatusBadRequest)
		return
	}

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Publish to Kafka when available; fall back to the in-process pipeline.
	if s.kafkaProducer != nil {
		partKey := kafka.PartitionKey(event.ID, event.Source)
		pubCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		err := s.kafkaProducer.Publish(pubCtx, kafka.TopicEventsRaw, partKey, event)
		cancel()
		if err != nil {
			log.Warn().Err(err).Str("event_id", event.ID).
				Msg("Kafka publish failed, falling back to in-process pipeline")
			// fallback ↓
		} else {
			metrics.IncEventsReceived("accepted")
			s.setRateLimitHeaders(w, clientID)
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{
				"status": "accepted",
				"id":     event.ID,
			})
			return
		}
	}

	// In-process fallback (also the default when Kafka is disabled).
	if err := s.processor.Submit(event); err != nil {
		metrics.IncEventsReceived("dropped")
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	metrics.IncEventsReceived("accepted")
	
	// Добавляем rate limit headers
	s.setRateLimitHeaders(w, clientID)
	
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "accepted",
		"id":     event.ID,
	})
}

func (s *HTTPServer) handleBatchEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Rate limiting для батчей
	clientID := getClientID(r)
	if s.rateLimiter != nil {
		// Для батчей проверяем заранее размер
		var events []models.Event
		if err := json.NewDecoder(r.Body).Decode(&events); err != nil {
			metrics.IncEventsReceived("error")
			http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
			return
		}

		if !s.rateLimiter.AllowN(clientID, len(events)) {
			metrics.IncEventsReceived("rate_limited")
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", s.getRateLimitInfo(clientID)))
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Rate limit exceeded for batch", http.StatusTooManyRequests)
			return
		}

		// Обрабатываем уже декодированный батч
		s.processBatchEvents(w, events, clientID)
		return
	}

	// Без rate limiting
	var events []models.Event
	if err := json.NewDecoder(r.Body).Decode(&events); err != nil {
		metrics.IncEventsReceived("error")
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	s.processBatchEvents(w, events, clientID)
}

func (s *HTTPServer) processBatchEvents(w http.ResponseWriter, events []models.Event, clientID string) {
	startTime := time.Now()
	defer func() {
		metrics.RecordIngestionLatency(time.Since(startTime).Seconds())
	}()

	if len(events) == 0 {
		http.Error(w, "Empty batch", http.StatusBadRequest)
		return
	}

	accepted := 0
	failed := 0

	for _, event := range events {
		if event.Timestamp.IsZero() {
			event.Timestamp = time.Now()
		}

		if err := s.processor.Submit(event); err != nil {
			failed++
		} else {
			accepted++
		}
	}

	metrics.IncBatchEventsReceived(accepted, failed)
	
	s.setRateLimitHeaders(w, clientID)

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "accepted",
		"accepted": accepted,
		"failed":   failed,
		"total":    len(events),
	})
}

// getClientID извлекает идентификатор клиента из запроса
func getClientID(r *http.Request) string {
	// Проверяем заголовок X-Client-ID
	if clientID := r.Header.Get("X-Client-ID"); clientID != "" {
		return clientID
	}

	// Проверяем source из query params
	if source := r.URL.Query().Get("source"); source != "" {
		return source
	}

	// Fallback на IP адрес
	return r.RemoteAddr
}

// setRateLimitHeaders устанавливает заголовки rate limit
func (s *HTTPServer) setRateLimitHeaders(w http.ResponseWriter, clientID string) {
	if s.rateLimiter == nil {
		return
	}

	limit, remaining := s.rateLimiter.GetLimit(clientID)
	w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
	w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
	w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(1*time.Second).Unix()))
}

func (s *HTTPServer) getRateLimitInfo(clientID string) int {
	if s.rateLimiter == nil {
		return 0
	}
	limit, _ := s.rateLimiter.GetLimit(clientID)
	return limit
}

func (s *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
	})
}

func (s *HTTPServer) handleReady(w http.ResponseWriter, r *http.Request) {
	if s.processor.IsReady() {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ready",
		})
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "not ready",
		})
	}
}

