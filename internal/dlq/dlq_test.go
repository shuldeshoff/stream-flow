package dlq

import (
	"context"
	"testing"
	"time"

	"github.com/sul/streamflow/internal/models"
)

func TestNewDeadLetterQueue(t *testing.T) {
	retryHandler := func(ctx context.Context, events []models.ProcessedEvent) error {
		return nil
	}

	dlq := NewDeadLetterQueue(retryHandler, 3, 1*time.Second)

	if dlq == nil {
		t.Fatal("Expected DLQ to be created")
	}

	// Graceful cleanup
	dlq.Stop()
}

func TestAddToQueue_Success(t *testing.T) {
	retryCount := 0
	retryHandler := func(ctx context.Context, events []models.ProcessedEvent) error {
		retryCount++
		return nil // Success on retry
	}

	dlq := NewDeadLetterQueue(retryHandler, 3, 100*time.Millisecond)
	defer dlq.Stop()

	events := []models.ProcessedEvent{
		{
			ID:     "evt-1",
			Type:   "test",
			Source: "test",
		},
	}

	dlq.Add(events, "Test error")

	// Ждем retry
	time.Sleep(300 * time.Millisecond)

	if retryCount == 0 {
		t.Error("Expected retry handler to be called")
	}
}

func TestRetryWithBackoff(t *testing.T) {
	attempts := 0
	retryHandler := func(ctx context.Context, events []models.ProcessedEvent) error {
		attempts++
		if attempts < 3 {
			return &tempError{msg: "temporary failure"}
		}
		return nil // Success on 3rd attempt
	}

	dlq := NewDeadLetterQueue(retryHandler, 5, 50*time.Millisecond)
	defer dlq.Stop()

	events := []models.ProcessedEvent{
		{ID: "evt-retry"},
	}

	dlq.Add(events, "Initial failure")

	// Ждем несколько попыток
	time.Sleep(500 * time.Millisecond)

	if attempts < 3 {
		t.Errorf("Expected at least 3 attempts, got %d", attempts)
	}
}

func TestMaxRetries(t *testing.T) {
	attempts := 0
	retryHandler := func(ctx context.Context, events []models.ProcessedEvent) error {
		attempts++
		return &tempError{msg: "always fails"}
	}

	maxRetries := 3
	dlq := NewDeadLetterQueue(retryHandler, maxRetries, 50*time.Millisecond)
	defer dlq.Stop()

	events := []models.ProcessedEvent{
		{ID: "evt-max-retry"},
	}

	dlq.Add(events, "Will fail")

	// Ждем достаточно времени для всех попыток
	time.Sleep(1 * time.Second)

	if attempts != maxRetries {
		t.Errorf("Expected exactly %d attempts, got %d", maxRetries, attempts)
	}

	// Проверяем, что событие в permanent failures
	stats := dlq.GetStats()
	if stats.PermanentFailures == 0 {
		t.Error("Expected event to be marked as permanent failure")
	}
}

func TestGetStats(t *testing.T) {
	retryHandler := func(ctx context.Context, events []models.ProcessedEvent) error {
		return nil
	}

	dlq := NewDeadLetterQueue(retryHandler, 3, 100*time.Millisecond)
	defer dlq.Stop()

	// Добавляем несколько событий
	for i := 0; i < 5; i++ {
		events := []models.ProcessedEvent{
			{ID: time.Now().Format("evt-20060102150405.000000")},
		}
		dlq.Add(events, "Test error")
	}

	time.Sleep(200 * time.Millisecond)

	stats := dlq.GetStats()

	if stats.TotalAdded != 5 {
		t.Errorf("Expected 5 total added, got %d", stats.TotalAdded)
	}

	if stats.Recovered == 0 {
		t.Error("Expected some events to be recovered")
	}
}

func TestConcurrentAdd(t *testing.T) {
	retryHandler := func(ctx context.Context, events []models.ProcessedEvent) error {
		return nil
	}

	dlq := NewDeadLetterQueue(retryHandler, 3, 100*time.Millisecond)
	defer dlq.Stop()

	// Конкурентно добавляем события
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			events := []models.ProcessedEvent{
				{ID: time.Now().Format("evt-20060102150405.000000")},
			}
			dlq.Add(events, "Concurrent test")
			done <- true
		}(i)
	}

	// Ждем завершения
	for i := 0; i < 10; i++ {
		<-done
	}

	time.Sleep(300 * time.Millisecond)

	stats := dlq.GetStats()
	if stats.TotalAdded != 10 {
		t.Errorf("Expected 10 total added, got %d", stats.TotalAdded)
	}
}

func TestStop(t *testing.T) {
	retryHandler := func(ctx context.Context, events []models.ProcessedEvent) error {
		time.Sleep(100 * time.Millisecond) // Simulate work
		return nil
	}

	dlq := NewDeadLetterQueue(retryHandler, 3, 50*time.Millisecond)

	events := []models.ProcessedEvent{
		{ID: "evt-stop-test"},
	}
	dlq.Add(events, "Test")

	// Останавливаем сразу
	dlq.Stop()

	// DLQ должен корректно завершиться без паники
}

func TestExponentialBackoff(t *testing.T) {
	attempts := []time.Time{}
	retryHandler := func(ctx context.Context, events []models.ProcessedEvent) error {
		attempts = append(attempts, time.Now())
		return &tempError{msg: "retry"}
	}

	dlq := NewDeadLetterQueue(retryHandler, 4, 50*time.Millisecond)
	defer dlq.Stop()

	events := []models.ProcessedEvent{
		{ID: "evt-backoff"},
	}

	dlq.Add(events, "Test backoff")

	// Ждем всех попыток
	time.Sleep(2 * time.Second)

	if len(attempts) < 4 {
		t.Fatalf("Expected at least 4 attempts, got %d", len(attempts))
	}

	// Проверяем, что интервалы увеличиваются
	for i := 1; i < len(attempts)-1; i++ {
		interval1 := attempts[i].Sub(attempts[i-1])
		interval2 := attempts[i+1].Sub(attempts[i])
		
		// Каждый следующий интервал должен быть больше или примерно равен
		// (с учетом jitter)
		if interval2 < interval1/2 {
			t.Errorf("Expected exponential backoff, but interval %d (%v) < interval %d (%v)",
				i+1, interval2, i, interval1)
		}
	}
}

func TestPermanentError(t *testing.T) {
	attempts := 0
	retryHandler := func(ctx context.Context, events []models.ProcessedEvent) error {
		attempts++
		return &permanentError{msg: "permanent failure"}
	}

	dlq := NewDeadLetterQueue(retryHandler, 5, 50*time.Millisecond)
	defer dlq.Stop()

	events := []models.ProcessedEvent{
		{ID: "evt-permanent"},
	}

	dlq.Add(events, "Will fail permanently")

	time.Sleep(300 * time.Millisecond)

	// Для permanent error не должно быть повторов
	if attempts > 1 {
		t.Errorf("Expected only 1 attempt for permanent error, got %d", attempts)
	}

	stats := dlq.GetStats()
	if stats.PermanentFailures == 0 {
		t.Error("Expected permanent failure to be recorded")
	}
}

// Helper error types
type tempError struct {
	msg string
}

func (e *tempError) Error() string {
	return e.msg
}

func (e *tempError) Temporary() bool {
	return true
}

type permanentError struct {
	msg string
}

func (e *permanentError) Error() string {
	return e.msg
}

func (e *permanentError) Permanent() bool {
	return true
}

func BenchmarkAdd(b *testing.B) {
	retryHandler := func(ctx context.Context, events []models.ProcessedEvent) error {
		return nil
	}

	dlq := NewDeadLetterQueue(retryHandler, 3, 100*time.Millisecond)
	defer dlq.Stop()

	events := []models.ProcessedEvent{
		{ID: "evt-bench"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dlq.Add(events, "Benchmark")
	}
}

func BenchmarkAddParallel(b *testing.B) {
	retryHandler := func(ctx context.Context, events []models.ProcessedEvent) error {
		return nil
	}

	dlq := NewDeadLetterQueue(retryHandler, 3, 100*time.Millisecond)
	defer dlq.Stop()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		events := []models.ProcessedEvent{
			{ID: "evt-bench-parallel"},
		}
		for pb.Next() {
			dlq.Add(events, "Benchmark")
		}
	})
}

