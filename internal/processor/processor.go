package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sul/streamflow/internal/cache"
	"github.com/sul/streamflow/internal/config"
	"github.com/sul/streamflow/internal/dlq"
	"github.com/sul/streamflow/internal/enrichment"
	"github.com/sul/streamflow/internal/metrics"
	"github.com/sul/streamflow/internal/models"
	"github.com/sul/streamflow/internal/storage"
)

type EventProcessor struct {
	config       config.ProcessorConfig
	storage      storage.Storage
	cache        *cache.RedisCache
	dlq          *dlq.DeadLetterQueue
	enrichment   *enrichment.Pipeline
	eventQueue   chan models.Event
	batchQueue   chan []models.ProcessedEvent
	wg           sync.WaitGroup
	ctx          context.Context
	cancel       context.CancelFunc
	workerPool   []*Worker
	isReady      bool
	readyMutex   sync.RWMutex
}

type Worker struct {
	id        int
	processor *EventProcessor
}

func NewEventProcessor(cfg config.ProcessorConfig, store storage.Storage, redisCache *cache.RedisCache) *EventProcessor {
	ctx, cancel := context.WithCancel(context.Background())
	
	// Создаем DLQ
	deadLetterQueue := dlq.NewDeadLetterQueue(store, 3, 5*time.Second)
	
	// Создаем enrichment pipeline
	enrichPipeline := enrichment.NewPipeline(redisCache)
	enrichPipeline.AddEnricher(&enrichment.TimestampEnricher{})
	enrichPipeline.AddEnricher(enrichment.NewGeoIPEnricher(redisCache))
	enrichPipeline.AddEnricher(&enrichment.UserAgentEnricher{})
	enrichPipeline.AddEnricher(enrichment.NewSessionEnricher(redisCache))
	enrichPipeline.AddEnricher(enrichment.NewCounterEnricher(redisCache))
	
	return &EventProcessor{
		config:     cfg,
		storage:    store,
		cache:      redisCache,
		dlq:        deadLetterQueue,
		enrichment: enrichPipeline,
		eventQueue: make(chan models.Event, cfg.BufferSize),
		batchQueue: make(chan []models.ProcessedEvent, 100),
		ctx:        ctx,
		cancel:     cancel,
		workerPool: make([]*Worker, cfg.WorkerCount),
		isReady:    false,
	}
}

func (ep *EventProcessor) Start() {
	log.Info().Int("workers", ep.config.WorkerCount).Msg("Starting event processor")

	// Запускаем воркеров для обработки событий
	for i := 0; i < ep.config.WorkerCount; i++ {
		worker := &Worker{
			id:        i,
			processor: ep,
		}
		ep.workerPool[i] = worker
		ep.wg.Add(1)
		go worker.run()
	}

	// Запускаем writer для записи батчей в БД
	ep.wg.Add(1)
	go ep.runWriter()

	ep.setReady(true)
	log.Info().Msg("Event processor ready")
}

func (ep *EventProcessor) Stop() {
	log.Info().Msg("Stopping event processor")
	ep.setReady(false)
	
	ep.cancel()
	
	// Закрываем очередь событий
	close(ep.eventQueue)
	
	// Останавливаем DLQ
	if ep.dlq != nil {
		ep.dlq.Stop()
	}
	
	// Ждем завершения всех воркеров
	ep.wg.Wait()
	
	log.Info().Msg("Event processor stopped")
}

func (ep *EventProcessor) Submit(event models.Event) error {
	select {
	case ep.eventQueue <- event:
		metrics.IncEventsQueued()
		return nil
	case <-time.After(100 * time.Millisecond):
		metrics.IncEventsDropped()
		return fmt.Errorf("queue is full")
	}
}

func (ep *EventProcessor) IsReady() bool {
	ep.readyMutex.RLock()
	defer ep.readyMutex.RUnlock()
	return ep.isReady
}

func (ep *EventProcessor) setReady(ready bool) {
	ep.readyMutex.Lock()
	defer ep.readyMutex.Unlock()
	ep.isReady = ready
}

func (w *Worker) run() {
	defer w.processor.wg.Done()
	
	log.Debug().Int("worker_id", w.id).Msg("Worker started")
	
	batch := make([]models.ProcessedEvent, 0, w.processor.config.BatchSize)
	ticker := time.NewTicker(time.Duration(w.processor.config.FlushTimeout) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case event, ok := <-w.processor.eventQueue:
			if !ok {
				// Очередь закрыта, отправляем остатки батча
				if len(batch) > 0 {
					w.processor.batchQueue <- batch
				}
				return
			}

			// Обрабатываем событие
			processed := w.processEvent(event)
			batch = append(batch, processed)

			// Если батч заполнен, отправляем на запись
			if len(batch) >= w.processor.config.BatchSize {
				w.processor.batchQueue <- batch
				batch = make([]models.ProcessedEvent, 0, w.processor.config.BatchSize)
				ticker.Reset(time.Duration(w.processor.config.FlushTimeout) * time.Second)
			}

		case <-ticker.C:
			// Таймаут - отправляем батч если есть события
			if len(batch) > 0 {
				w.processor.batchQueue <- batch
				batch = make([]models.ProcessedEvent, 0, w.processor.config.BatchSize)
			}

		case <-w.processor.ctx.Done():
			// Graceful shutdown
			if len(batch) > 0 {
				w.processor.batchQueue <- batch
			}
			return
		}
	}
}

func (w *Worker) processEvent(event models.Event) models.ProcessedEvent {
	startTime := time.Now()
	defer func() {
		metrics.RecordProcessingLatency(time.Since(startTime).Seconds())
		metrics.IncEventsProcessed()
	}()

	// Enrichment pipeline
	if w.processor.enrichment != nil {
		enrichCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		if err := w.processor.enrichment.Process(enrichCtx, &event); err != nil {
			log.Warn().Err(err).Str("event_id", event.ID).Msg("Enrichment failed")
			// Продолжаем даже если enrichment failed
		}
		cancel()
	}

	// Обновляем статистику в Redis
	if w.processor.cache != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		// Инкрементируем счетчики по типу события и источнику
		w.processor.cache.IncrementEventTypeStats(ctx, event.Type, 1*time.Minute)
		w.processor.cache.IncrementSourceStats(ctx, event.Source, 1*time.Minute)
	}

	// Сериализуем данные и метаданные в JSON
	dataJSON, _ := json.Marshal(event.Data)
	metadataJSON, _ := json.Marshal(event.Metadata)

	return models.ProcessedEvent{
		ID:          event.ID,
		Type:        event.Type,
		Source:      event.Source,
		Timestamp:   event.Timestamp,
		ProcessedAt: time.Now(),
		Data:        string(dataJSON),
		Metadata:    string(metadataJSON),
	}
}

func (ep *EventProcessor) runWriter() {
	defer ep.wg.Done()
	defer close(ep.batchQueue)
	
	log.Debug().Msg("Writer started")

	for {
		select {
		case batch := <-ep.batchQueue:
			if err := ep.storage.WriteBatch(ep.ctx, batch); err != nil {
				log.Error().Err(err).Int("batch_size", len(batch)).Msg("Failed to write batch")
				metrics.IncBatchWriteErrors()
				
				// Отправляем события в DLQ
				if ep.dlq != nil {
					for _, processed := range batch {
						event := models.Event{
							ID:        processed.ID,
							Type:      processed.Type,
							Source:    processed.Source,
							Timestamp: processed.Timestamp,
						}
						ep.dlq.AddFailedEvent(event, err, "storage_write")
					}
				}
			} else {
				metrics.IncBatchesWritten(len(batch))
			}

		case <-ep.ctx.Done():
			// Обрабатываем оставшиеся батчи
			for {
				select {
				case batch := <-ep.batchQueue:
					if err := ep.storage.WriteBatch(ep.ctx, batch); err != nil {
						log.Error().Err(err).Msg("Failed to write remaining batch")
					}
				default:
					return
				}
			}
		}
	}
}
