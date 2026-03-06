package query

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/shuldeshoff/stream-flow/internal/cache"
	"github.com/shuldeshoff/stream-flow/internal/config"
	"github.com/shuldeshoff/stream-flow/internal/storage"
)

type QueryServer struct {
	config  config.ServerConfig
	storage storage.Storage
	cache   *cache.RedisCache
	server  *http.Server
}

func NewQueryServer(cfg config.ServerConfig, store storage.Storage, cache *cache.RedisCache) *QueryServer {
	return &QueryServer{
		config:  cfg,
		storage: store,
		cache:   cache,
	}
}

func (s *QueryServer) Start() error {
	mux := http.NewServeMux()

	// Query endpoints
	mux.HandleFunc("/api/v1/query/events", s.handleQueryEvents)
	mux.HandleFunc("/api/v1/query/stats", s.handleQueryStats)
	mux.HandleFunc("/api/v1/query/stats/types", s.handleQueryTypeStats)
	mux.HandleFunc("/api/v1/query/stats/sources", s.handleQuerySourceStats)

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.config.Port+1), // Query API на порту +1 от основного
		Handler:      mux,
		ReadTimeout:  time.Duration(s.config.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(s.config.WriteTimeout) * time.Second,
	}

	log.Info().Msgf("Query API server listening on :%d", s.config.Port+1)
	return s.server.ListenAndServe()
}

func (s *QueryServer) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func (s *QueryServer) handleQueryEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Парсим параметры запроса
	query := r.URL.Query()
	eventType := query.Get("type")
	source := query.Get("source")
	limit := query.Get("limit")

	// TODO: Реализовать реальный запрос к ClickHouse
	// Пока возвращаем заглушку

	response := map[string]interface{}{
		"query": map[string]string{
			"type":   eventType,
			"source": source,
			"limit":  limit,
		},
		"message": "Query API - Coming soon! This will fetch events from ClickHouse",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *QueryServer) handleQueryStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Получаем статистику из Redis
	ctx := r.Context()

	windowParam := r.URL.Query().Get("window")
	window := 1 * time.Minute // default
	if windowParam != "" {
		duration, err := strconv.Atoi(windowParam)
		if err == nil {
			window = time.Duration(duration) * time.Second
		}
	}

	typeStats, err := s.cache.GetAllEventTypeStats(ctx, window)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get stats from cache")
		typeStats = make(map[string]int64)
	}

	response := map[string]interface{}{
		"window":     window.String(),
		"type_stats": typeStats,
		"timestamp":  time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *QueryServer) handleQueryTypeStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	eventType := r.URL.Query().Get("type")

	if eventType == "" {
		http.Error(w, "Event type is required", http.StatusBadRequest)
		return
	}

	window := 1 * time.Minute
	count, err := s.cache.GetEventTypeStats(ctx, eventType, window)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get type stats")
		count = 0
	}

	response := map[string]interface{}{
		"type":   eventType,
		"window": window.String(),
		"count":  count,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *QueryServer) handleQuerySourceStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	source := r.URL.Query().Get("source")

	if source == "" {
		http.Error(w, "Source is required", http.StatusBadRequest)
		return
	}

	window := 1 * time.Minute
	count, err := s.cache.GetSourceStats(ctx, source, window)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get source stats")
		count = 0
	}

	response := map[string]interface{}{
		"source": source,
		"window": window.String(),
		"count":  count,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

