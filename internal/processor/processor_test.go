package processor

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shuldeshoff/stream-flow/internal/config"
	"github.com/shuldeshoff/stream-flow/internal/models"
)

type MockStorage struct {
	batches [][]models.ProcessedEvent
}

func (m *MockStorage) WriteBatch(ctx context.Context, events []models.ProcessedEvent) error {
	m.batches = append(m.batches, events)
	return nil
}

func (m *MockStorage) Close() error {
	return nil
}

func TestEventProcessor(t *testing.T) {
	// Создаем mock storage
	mockStore := &MockStorage{
		batches: make([][]models.ProcessedEvent, 0),
	}

	// Конфигурация для тестов
	cfg := config.ProcessorConfig{
		WorkerCount:  2,
		BufferSize:   100,
		BatchSize:    10,
		FlushTimeout: 1,
	}

	// Создаем процессор
	processor := NewEventProcessor(cfg, mockStore, nil)
	processor.Start()
	defer processor.Stop()

	// Проверяем, что процессор готов
	if !processor.IsReady() {
		t.Fatal("Processor should be ready")
	}

	// Отправляем тестовые события
	for i := 0; i < 25; i++ {
		event := models.Event{
			ID:        fmt.Sprintf("test-%d", i),
			Type:      "test_event",
			Source:    "test_suite",
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"value": i,
			},
		}

		if err := processor.Submit(event); err != nil {
			t.Fatalf("Failed to submit event: %v", err)
		}
	}

	// Ждем обработки
	time.Sleep(2 * time.Second)

	// Проверяем, что события были обработаны и записаны
	if len(mockStore.batches) == 0 {
		t.Fatal("No batches were written")
	}

	totalEvents := 0
	for _, batch := range mockStore.batches {
		totalEvents += len(batch)
	}

	if totalEvents != 25 {
		t.Fatalf("Expected 25 events, got %d", totalEvents)
	}
}

func BenchmarkEventProcessor(b *testing.B) {
	mockStore := &MockStorage{
		batches: make([][]models.ProcessedEvent, 0),
	}

	cfg := config.ProcessorConfig{
		WorkerCount:  10,
		BufferSize:   10000,
		BatchSize:    1000,
		FlushTimeout: 5,
	}

	processor := NewEventProcessor(cfg, mockStore, nil)
	processor.Start()
	defer processor.Stop()

	event := models.Event{
		ID:        "bench-event",
		Type:      "benchmark",
		Source:    "bench_suite",
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"key": "value",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		processor.Submit(event)
	}
}

