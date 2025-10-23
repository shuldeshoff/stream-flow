package models

import "time"

// Event представляет входящее событие
type Event struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Source    string                 `json:"source"`
	Timestamp time.Time              `json:"timestamp"`
	Data      map[string]interface{} `json:"data"`
	Metadata  map[string]string      `json:"metadata,omitempty"`
}

// ProcessedEvent представляет обработанное событие готовое к сохранению
type ProcessedEvent struct {
	ID          string
	Type        string
	Source      string
	Timestamp   time.Time
	ProcessedAt time.Time
	Data        string // JSON string
	Metadata    string // JSON string
}

