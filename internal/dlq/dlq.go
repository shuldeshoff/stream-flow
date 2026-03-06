package dlq

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/shuldeshoff/stream-flow/internal/models"
	"github.com/shuldeshoff/stream-flow/internal/storage"
)

// DeadLetterQueue управляет необработанными событиями
type DeadLetterQueue struct {
	storage    storage.Storage
	queue      chan FailedEvent
	retryQueue chan FailedEvent
	mu         sync.RWMutex
	stats      DLQStats
	maxRetries int
	retryDelay time.Duration
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

type FailedEvent struct {
	Event        models.Event
	Error        string
	FailedAt     time.Time
	RetryCount   int
	LastRetryAt  time.Time
	Source       string // источник ошибки (processor, storage, etc)
}

type DLQStats struct {
	TotalFailed    int64
	TotalRetried   int64
	TotalRecovered int64
	CurrentSize    int
	mu             sync.RWMutex
}

func NewDeadLetterQueue(store storage.Storage, maxRetries int, retryDelay time.Duration) *DeadLetterQueue {
	ctx, cancel := context.WithCancel(context.Background())
	
	dlq := &DeadLetterQueue{
		storage:    store,
		queue:      make(chan FailedEvent, 10000),
		retryQueue: make(chan FailedEvent, 1000),
		maxRetries: maxRetries,
		retryDelay: retryDelay,
		ctx:        ctx,
		cancel:     cancel,
	}

	// Запускаем обработчики
	dlq.wg.Add(2)
	go dlq.processFailedEvents()
	go dlq.retryFailedEvents()

	return dlq
}

// AddFailedEvent добавляет событие в DLQ
func (dlq *DeadLetterQueue) AddFailedEvent(event models.Event, err error, source string) {
	failed := FailedEvent{
		Event:       event,
		Error:       err.Error(),
		FailedAt:    time.Now(),
		RetryCount:  0,
		Source:      source,
	}

	select {
	case dlq.queue <- failed:
		dlq.stats.mu.Lock()
		dlq.stats.TotalFailed++
		dlq.stats.CurrentSize++
		dlq.stats.mu.Unlock()
		
		log.Warn().
			Str("event_id", event.ID).
			Str("error", err.Error()).
			Str("source", source).
			Msg("Event added to DLQ")
	default:
		log.Error().
			Str("event_id", event.ID).
			Msg("DLQ is full, dropping event")
	}
}

// processFailedEvents обрабатывает события из DLQ
func (dlq *DeadLetterQueue) processFailedEvents() {
	defer dlq.wg.Done()

	for {
		select {
		case failed := <-dlq.queue:
			dlq.handleFailedEvent(failed)

		case <-dlq.ctx.Done():
			// Обрабатываем оставшиеся события
			for {
				select {
				case failed := <-dlq.queue:
					dlq.handleFailedEvent(failed)
				default:
					return
				}
			}
		}
	}
}

func (dlq *DeadLetterQueue) handleFailedEvent(failed FailedEvent) {
	// Логируем в отдельную таблицу ClickHouse
	if err := dlq.logToStorage(failed); err != nil {
		log.Error().Err(err).Str("event_id", failed.Event.ID).Msg("Failed to log to DLQ storage")
	}

	// Если не достигли лимита retry, добавляем в очередь повтора
	if failed.RetryCount < dlq.maxRetries {
		dlq.scheduleRetry(failed)
	} else {
		log.Error().
			Str("event_id", failed.Event.ID).
			Int("retries", failed.RetryCount).
			Msg("Event exhausted all retries")
	}

	dlq.stats.mu.Lock()
	dlq.stats.CurrentSize--
	dlq.stats.mu.Unlock()
}

func (dlq *DeadLetterQueue) scheduleRetry(failed FailedEvent) {
	// Увеличиваем счетчик retry
	failed.RetryCount++
	failed.LastRetryAt = time.Now()

	// Отправляем в очередь повтора с задержкой
	go func() {
		delay := dlq.retryDelay * time.Duration(failed.RetryCount) // экспоненциальная задержка
		
		select {
		case <-time.After(delay):
			select {
			case dlq.retryQueue <- failed:
				dlq.stats.mu.Lock()
				dlq.stats.TotalRetried++
				dlq.stats.mu.Unlock()
			case <-dlq.ctx.Done():
				return
			}
		case <-dlq.ctx.Done():
			return
		}
	}()
}

func (dlq *DeadLetterQueue) retryFailedEvents() {
	defer dlq.wg.Done()

	for {
		select {
		case failed := <-dlq.retryQueue:
			// Пытаемся повторно обработать событие
			if err := dlq.retryEvent(failed); err != nil {
				// Если снова ошибка, возвращаем в DLQ
				dlq.AddFailedEvent(failed.Event, err, fmt.Sprintf("%s_retry", failed.Source))
			} else {
				dlq.stats.mu.Lock()
				dlq.stats.TotalRecovered++
				dlq.stats.mu.Unlock()

				log.Info().
					Str("event_id", failed.Event.ID).
					Int("retry_count", failed.RetryCount).
					Msg("Event recovered from DLQ")
			}

		case <-dlq.ctx.Done():
			return
		}
	}
}

func (dlq *DeadLetterQueue) retryEvent(failed FailedEvent) error {
	// Попытка записи в основное хранилище
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dataJSON, _ := json.Marshal(failed.Event.Data)
	metadataJSON, _ := json.Marshal(failed.Event.Metadata)

	processed := models.ProcessedEvent{
		ID:          failed.Event.ID,
		Type:        failed.Event.Type,
		Source:      failed.Event.Source,
		Timestamp:   failed.Event.Timestamp,
		ProcessedAt: time.Now(),
		Data:        string(dataJSON),
		Metadata:    string(metadataJSON),
	}

	return dlq.storage.WriteBatch(ctx, []models.ProcessedEvent{processed})
}

func (dlq *DeadLetterQueue) logToStorage(failed FailedEvent) error {
	// Записываем в специальную DLQ таблицу
	// TODO: создать отдельную таблицу для DLQ в ClickHouse
	log.Debug().
		Str("event_id", failed.Event.ID).
		Str("error", failed.Error).
		Int("retry_count", failed.RetryCount).
		Msg("Logged to DLQ storage")
	
	return nil
}

// GetStats возвращает статистику DLQ
func (dlq *DeadLetterQueue) GetStats() DLQStats {
	dlq.stats.mu.RLock()
	defer dlq.stats.mu.RUnlock()

	return DLQStats{
		TotalFailed:    dlq.stats.TotalFailed,
		TotalRetried:   dlq.stats.TotalRetried,
		TotalRecovered: dlq.stats.TotalRecovered,
		CurrentSize:    dlq.stats.CurrentSize,
	}
}

// GetFailedEvents возвращает список недавних failed событий
func (dlq *DeadLetterQueue) GetFailedEvents(limit int) []FailedEvent {
	// TODO: реализовать получение из storage
	return []FailedEvent{}
}

// ReprocessEvent пытается повторно обработать конкретное событие
func (dlq *DeadLetterQueue) ReprocessEvent(eventID string) error {
	// TODO: реализовать поиск события в storage и повторную обработку
	return fmt.Errorf("not implemented")
}

// Clear очищает DLQ
func (dlq *DeadLetterQueue) Clear() {
	// Очищаем очереди
	for {
		select {
		case <-dlq.queue:
		default:
			goto ClearRetry
		}
	}

ClearRetry:
	for {
		select {
		case <-dlq.retryQueue:
		default:
			dlq.stats.mu.Lock()
			dlq.stats.CurrentSize = 0
			dlq.stats.mu.Unlock()
			return
		}
	}
}

// Stop останавливает DLQ
func (dlq *DeadLetterQueue) Stop() {
	log.Info().Msg("Stopping Dead Letter Queue")
	dlq.cancel()
	
	// Закрываем очереди
	close(dlq.queue)
	close(dlq.retryQueue)
	
	// Ждем завершения обработчиков
	dlq.wg.Wait()
	
	log.Info().Msg("Dead Letter Queue stopped")
}

