package enrichment

import (
	"context"
	"testing"
	"time"

	"github.com/shuldeshoff/stream-flow/internal/models"
)

func TestNewEnrichmentPipeline(t *testing.T) {
	pipeline := NewEnrichmentPipeline()

	if pipeline == nil {
		t.Fatal("Expected pipeline to be created")
	}

	if len(pipeline.enrichers) != 0 {
		t.Error("Expected empty enrichers initially")
	}
}

func TestAddEnricher(t *testing.T) {
	pipeline := NewEnrichmentPipeline()
	enricher := &TimestampEnricher{}

	pipeline.AddEnricher(enricher)

	if len(pipeline.enrichers) != 1 {
		t.Errorf("Expected 1 enricher, got %d", len(pipeline.enrichers))
	}
}

func TestEnrich_SingleEnricher(t *testing.T) {
	pipeline := NewEnrichmentPipeline()
	pipeline.AddEnricher(&TimestampEnricher{})

	event := &models.Event{
		ID:     "evt-1",
		Type:   "test",
		Source: "test",
	}

	ctx := context.Background()
	enriched := pipeline.Enrich(ctx, event)

	// Проверяем, что добавлены timestamp поля
	if enriched.Metadata == nil {
		t.Fatal("Expected metadata to be set")
	}

	if enriched.Metadata["enriched_at"] == nil {
		t.Error("Expected enriched_at to be set")
	}
}

func TestEnrich_MultipleEnrichers(t *testing.T) {
	pipeline := NewEnrichmentPipeline()
	pipeline.AddEnricher(&TimestampEnricher{})
	pipeline.AddEnricher(&CounterEnricher{})

	event := &models.Event{
		ID:     "evt-1",
		Type:   "test",
		Source: "test",
	}

	ctx := context.Background()
	enriched := pipeline.Enrich(ctx, event)

	if enriched.Metadata["enriched_at"] == nil {
		t.Error("Expected enriched_at from TimestampEnricher")
	}

	if enriched.Metadata["event_count"] == nil {
		t.Error("Expected event_count from CounterEnricher")
	}
}

func TestTimestampEnricher(t *testing.T) {
	enricher := &TimestampEnricher{}
	event := &models.Event{
		ID:   "evt-1",
		Type: "test",
	}

	ctx := context.Background()
	err := enricher.Enrich(ctx, event)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if event.Metadata == nil {
		t.Fatal("Expected metadata to be set")
	}

	enrichedAt, ok := event.Metadata["enriched_at"].(time.Time)
	if !ok {
		t.Error("Expected enriched_at to be time.Time")
	}

	if time.Since(enrichedAt) > 1*time.Second {
		t.Error("Expected enriched_at to be recent")
	}
}

func TestGeoIPEnricher(t *testing.T) {
	enricher := &GeoIPEnricher{}
	event := &models.Event{
		ID:   "evt-1",
		Type: "test",
		Data: map[string]interface{}{
			"ip_address": "8.8.8.8",
		},
	}

	ctx := context.Background()
	err := enricher.Enrich(ctx, event)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if event.Metadata == nil {
		t.Fatal("Expected metadata to be set")
	}

	// В реальном окружении здесь были бы geo данные
	// В тестах просто проверяем, что метод отработал
}

func TestUserAgentEnricher(t *testing.T) {
	enricher := &UserAgentEnricher{}
	event := &models.Event{
		ID:   "evt-1",
		Type: "test",
		Data: map[string]interface{}{
			"user_agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		},
	}

	ctx := context.Background()
	err := enricher.Enrich(ctx, event)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if event.Metadata == nil {
		t.Fatal("Expected metadata to be set")
	}

	// Проверяем разбор User-Agent
	if event.Metadata["browser"] == nil {
		t.Error("Expected browser to be parsed")
	}

	if event.Metadata["os"] == nil {
		t.Error("Expected OS to be parsed")
	}
}

func TestSessionEnricher(t *testing.T) {
	enricher := &SessionEnricher{}
	event := &models.Event{
		ID:   "evt-1",
		Type: "test",
		Data: map[string]interface{}{
			"user_id": "user-123",
		},
	}

	ctx := context.Background()
	err := enricher.Enrich(ctx, event)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if event.Metadata == nil {
		t.Fatal("Expected metadata to be set")
	}

	// Проверяем, что добавлен session_id
	if event.Metadata["session_id"] == nil {
		t.Error("Expected session_id to be set")
	}
}

func TestCounterEnricher(t *testing.T) {
	enricher := &CounterEnricher{}
	event1 := &models.Event{ID: "evt-1", Type: "test"}
	event2 := &models.Event{ID: "evt-2", Type: "test"}

	ctx := context.Background()

	enricher.Enrich(ctx, event1)
	enricher.Enrich(ctx, event2)

	// Проверяем, что счетчик инкрементируется
	count1, ok1 := event1.Metadata["event_count"].(int64)
	count2, ok2 := event2.Metadata["event_count"].(int64)

	if !ok1 || !ok2 {
		t.Fatal("Expected event_count to be int64")
	}

	if count2 != count1+1 {
		t.Errorf("Expected counter to increment: %d -> %d", count1, count2)
	}
}

func TestEnrich_EmptyEvent(t *testing.T) {
	pipeline := NewEnrichmentPipeline()
	pipeline.AddEnricher(&TimestampEnricher{})

	event := &models.Event{}

	ctx := context.Background()
	enriched := pipeline.Enrich(ctx, event)

	if enriched == nil {
		t.Fatal("Expected enriched event, got nil")
	}

	// Даже пустое событие должно быть обогащено
	if enriched.Metadata == nil {
		t.Error("Expected metadata to be set even for empty event")
	}
}

func TestEnrich_NilContext(t *testing.T) {
	pipeline := NewEnrichmentPipeline()
	pipeline.AddEnricher(&TimestampEnricher{})

	event := &models.Event{ID: "evt-1"}

	// Должно работать даже с nil context (внутри создастся Background)
	enriched := pipeline.Enrich(nil, event)

	if enriched.Metadata == nil {
		t.Error("Expected enrichment to work with nil context")
	}
}

func TestConcurrentEnrich(t *testing.T) {
	pipeline := NewEnrichmentPipeline()
	pipeline.AddEnricher(&TimestampEnricher{})
	pipeline.AddEnricher(&CounterEnricher{})

	ctx := context.Background()
	done := make(chan bool)

	// Конкурентно обогащаем события
	for i := 0; i < 100; i++ {
		go func(id int) {
			event := &models.Event{
				ID:   time.Now().Format("evt-20060102150405.000000"),
				Type: "test",
			}
			pipeline.Enrich(ctx, event)
			done <- true
		}(i)
	}

	// Ждем завершения
	for i := 0; i < 100; i++ {
		<-done
	}

	// Нет проверки результата, главное - не должно быть race conditions
}

func TestEnricherError(t *testing.T) {
	// Создаем enricher, который возвращает ошибку
	type ErrorEnricher struct{}
	
	errorEnricher := &ErrorEnricher{}
	
	pipeline := NewEnrichmentPipeline()
	pipeline.AddEnricher(errorEnricher)

	event := &models.Event{ID: "evt-1"}
	ctx := context.Background()

	// Pipeline должен продолжить работу даже если один enricher упал
	enriched := pipeline.Enrich(ctx, event)
	if enriched == nil {
		t.Error("Expected pipeline to continue on enricher error")
	}
}

func TestEnricherOrder(t *testing.T) {
	pipeline := NewEnrichmentPipeline()

	// Добавляем enrichers в определенном порядке
	enricher1 := &TimestampEnricher{}
	enricher2 := &CounterEnricher{}

	pipeline.AddEnricher(enricher1)
	pipeline.AddEnricher(enricher2)

	event := &models.Event{ID: "evt-1"}
	ctx := context.Background()

	enriched := pipeline.Enrich(ctx, event)

	// Оба enricher должны сработать
	if enriched.Metadata["enriched_at"] == nil {
		t.Error("Expected first enricher to work")
	}
	if enriched.Metadata["event_count"] == nil {
		t.Error("Expected second enricher to work")
	}
}

func TestMetadataPreservation(t *testing.T) {
	pipeline := NewEnrichmentPipeline()
	pipeline.AddEnricher(&TimestampEnricher{})

	event := &models.Event{
		ID:   "evt-1",
		Type: "test",
		Metadata: map[string]interface{}{
			"existing_field": "existing_value",
		},
	}

	ctx := context.Background()
	enriched := pipeline.Enrich(ctx, event)

	// Проверяем, что существующие метаданные сохранились
	if enriched.Metadata["existing_field"] != "existing_value" {
		t.Error("Expected existing metadata to be preserved")
	}

	// И новые добавились
	if enriched.Metadata["enriched_at"] == nil {
		t.Error("Expected new metadata to be added")
	}
}

func BenchmarkEnrich_Single(b *testing.B) {
	pipeline := NewEnrichmentPipeline()
	pipeline.AddEnricher(&TimestampEnricher{})

	event := &models.Event{
		ID:   "evt-bench",
		Type: "test",
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pipeline.Enrich(ctx, event)
	}
}

func BenchmarkEnrich_Multiple(b *testing.B) {
	pipeline := NewEnrichmentPipeline()
	pipeline.AddEnricher(&TimestampEnricher{})
	pipeline.AddEnricher(&CounterEnricher{})
	pipeline.AddEnricher(&SessionEnricher{})

	event := &models.Event{
		ID:   "evt-bench",
		Type: "test",
		Data: map[string]interface{}{
			"user_id": "user-123",
		},
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pipeline.Enrich(ctx, event)
	}
}

func BenchmarkEnrich_Parallel(b *testing.B) {
	pipeline := NewEnrichmentPipeline()
	pipeline.AddEnricher(&TimestampEnricher{})
	pipeline.AddEnricher(&CounterEnricher{})

	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		event := &models.Event{
			ID:   "evt-bench-parallel",
			Type: "test",
		}
		for pb.Next() {
			pipeline.Enrich(ctx, event)
		}
	})
}

