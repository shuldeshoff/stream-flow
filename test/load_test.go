package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type Event struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Source    string                 `json:"source"`
	Timestamp time.Time              `json:"timestamp"`
	Data      map[string]interface{} `json:"data"`
}

func main() {
	baseURL := "http://localhost:8080"
	
	// Проверяем health
	fmt.Println("🔍 Checking health...")
	if err := checkHealth(baseURL); err != nil {
		fmt.Printf("❌ Health check failed: %v\n", err)
		return
	}
	fmt.Println("✅ Service is healthy")

	// Тест 1: Одиночное событие
	fmt.Println("\n📤 Test 1: Sending single event...")
	if err := sendSingleEvent(baseURL); err != nil {
		fmt.Printf("❌ Failed: %v\n", err)
	} else {
		fmt.Println("✅ Single event sent successfully")
	}

	// Тест 2: Батч событий
	fmt.Println("\n📦 Test 2: Sending batch of events...")
	if err := sendBatchEvents(baseURL, 100); err != nil {
		fmt.Printf("❌ Failed: %v\n", err)
	} else {
		fmt.Println("✅ Batch events sent successfully")
	}

	// Тест 3: Load test
	fmt.Println("\n🔥 Test 3: Load testing (10K events)...")
	startTime := time.Now()
	if err := loadTest(baseURL, 10000, 100); err != nil {
		fmt.Printf("❌ Failed: %v\n", err)
	} else {
		duration := time.Since(startTime)
		rps := float64(10000) / duration.Seconds()
		fmt.Printf("✅ Load test completed in %v (%.0f events/sec)\n", duration, rps)
	}

	fmt.Println("\n✨ All tests completed!")
}

func checkHealth(baseURL string) error {
	resp, err := http.Get(baseURL + "/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	return nil
}

func sendSingleEvent(baseURL string) error {
	event := Event{
		ID:        "test-single-1",
		Type:      "page_view",
		Source:    "load_test",
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"url":      "/home",
			"user_id":  "user123",
			"duration": 5.2,
		},
	}

	return sendEvent(baseURL+"/api/v1/events", event)
}

func sendBatchEvents(baseURL string, count int) error {
	events := make([]Event, count)
	for i := 0; i < count; i++ {
		events[i] = Event{
			ID:        fmt.Sprintf("test-batch-%d", i),
			Type:      "user_action",
			Source:    "load_test",
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"action": "click",
				"target": fmt.Sprintf("button-%d", i%10),
			},
		}
	}

	jsonData, err := json.Marshal(events)
	if err != nil {
		return err
	}

	resp, err := http.Post(
		baseURL+"/api/v1/events/batch",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	return nil
}

func loadTest(baseURL string, totalEvents, concurrency int) error {
	var wg sync.WaitGroup
	eventsPerWorker := totalEvents / concurrency
	errChan := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := 0; j < eventsPerWorker; j++ {
				event := Event{
					ID:        fmt.Sprintf("load-%d-%d", workerID, j),
					Type:      "load_test",
					Source:    fmt.Sprintf("worker-%d", workerID),
					Timestamp: time.Now(),
					Data: map[string]interface{}{
						"worker":  workerID,
						"counter": j,
						"random":  time.Now().UnixNano(),
					},
				}

				if err := sendEvent(baseURL+"/api/v1/events", event); err != nil {
					errChan <- err
					return
				}
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	if err := <-errChan; err != nil {
		return err
	}

	return nil
}

func sendEvent(url string, event Event) error {
	jsonData, err := json.Marshal(event)
	if err != nil {
		return err
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	return nil
}

