package ingestion

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sul/streamflow/internal/config"
	"github.com/sul/streamflow/internal/metrics"
	"github.com/sul/streamflow/internal/models"
	"github.com/sul/streamflow/internal/processor"
)

type HTTPServer struct {
	config    config.ServerConfig
	processor *processor.EventProcessor
	server    *http.Server
}

func NewHTTPServer(cfg config.ServerConfig, proc *processor.EventProcessor) *HTTPServer {
	return &HTTPServer{
		config:    cfg,
		processor: proc,
	}
}

func (s *HTTPServer) Start() error {
	mux := http.NewServeMux()
	
	// Основной endpoint для приема событий
	mux.HandleFunc("/api/v1/events", s.handleEvents)
	mux.HandleFunc("/api/v1/events/batch", s.handleBatchEvents)
	
	// Health check
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ready", s.handleReady)

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.config.Port),
		Handler:      mux,
		ReadTimeout:  time.Duration(s.config.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(s.config.WriteTimeout) * time.Second,
	}

	log.Info().Msgf("HTTP server listening on :%d", s.config.Port)
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

	// Отправляем событие на обработку
	if err := s.processor.Submit(event); err != nil {
		metrics.IncEventsReceived("dropped")
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	metrics.IncEventsReceived("accepted")
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

	startTime := time.Now()
	defer func() {
		metrics.RecordIngestionLatency(time.Since(startTime).Seconds())
	}()

	var events []models.Event
	if err := json.NewDecoder(r.Body).Decode(&events); err != nil {
		metrics.IncEventsReceived("error")
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

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

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "accepted",
		"accepted": accepted,
		"failed":   failed,
		"total":    len(events),
	})
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

