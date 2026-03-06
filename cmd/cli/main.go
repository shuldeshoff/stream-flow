package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const Version = "0.2.0"

func main() {
	// Subcommands
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "health":
		checkHealth()
	case "stats":
		getStats()
	case "send":
		sendEvent()
	case "version":
		fmt.Printf("StreamFlow CLI v%s\n", Version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Printf("Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("StreamFlow CLI - управление StreamFlow сервером")
	fmt.Println("\nИспользование:")
	fmt.Println("  streamflow-cli <command> [options]")
	fmt.Println("\nКоманды:")
	fmt.Println("  health              Проверить состояние сервера")
	fmt.Println("  stats [--window]    Получить статистику событий")
	fmt.Println("  send <event.json>   Отправить событие из JSON файла")
	fmt.Println("  version             Показать версию CLI")
	fmt.Println("  help                Показать эту справку")
	fmt.Println("\nПеременные окружения:")
	fmt.Println("  STREAMFLOW_URL      URL сервера (default: http://localhost:8080)")
	fmt.Println("\nПримеры:")
	fmt.Println("  streamflow-cli health")
	fmt.Println("  streamflow-cli stats --window=60")
	fmt.Println("  streamflow-cli send event.json")
}

func getBaseURL() string {
	url := os.Getenv("STREAMFLOW_URL")
	if url == "" {
		url = "http://localhost:8080"
	}
	return url
}

func getQueryBaseURL() string {
	url := os.Getenv("STREAMFLOW_QUERY_URL")
	if url == "" {
		url = "http://localhost:8081"
	}
	return url
}

func checkHealth() {
	url := getBaseURL() + "/health"
	
	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("❌ Ошибка подключения: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	
	if resp.StatusCode == http.StatusOK {
		fmt.Println("✅ Сервер работает")
		fmt.Printf("   Status: %d\n", resp.StatusCode)
		fmt.Printf("   Response: %s\n", string(body))
	} else {
		fmt.Printf("⚠️  Сервер вернул статус: %d\n", resp.StatusCode)
		os.Exit(1)
	}
}

func getStats() {
	statsCmd := flag.NewFlagSet("stats", flag.ExitOnError)
	window := statsCmd.Int("window", 60, "Временное окно в секундах")
	statsCmd.Parse(os.Args[2:])

	url := fmt.Sprintf("%s/api/v1/query/stats?window=%d", getQueryBaseURL(), *window)
	
	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("❌ Ошибка подключения к Query API: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	
	if resp.StatusCode == http.StatusOK {
		var result map[string]interface{}
		json.Unmarshal(body, &result)
		
		fmt.Println("📊 Статистика событий")
		fmt.Printf("   Временное окно: %v\n", result["window"])
		fmt.Printf("   Время запроса: %v\n", result["timestamp"])
		
		if typeStats, ok := result["type_stats"].(map[string]interface{}); ok {
			fmt.Println("\n   События по типам:")
			for eventType, count := range typeStats {
				fmt.Printf("     %s: %.0f\n", eventType, count)
			}
		}
	} else {
		fmt.Printf("⚠️  Query API вернул статус: %d\n", resp.StatusCode)
		fmt.Printf("   Response: %s\n", string(body))
	}
}

func sendEvent() {
	if len(os.Args) < 3 {
		fmt.Println("❌ Необходимо указать файл с событием")
		fmt.Println("   Использование: streamflow-cli send event.json")
		os.Exit(1)
	}

	filename := os.Args[2]
	
	file, err := os.ReadFile(filename)
	if err != nil {
		fmt.Printf("❌ Ошибка чтения файла: %v\n", err)
		os.Exit(1)
	}

	// Проверяем, что это валидный JSON
	var event map[string]interface{}
	if err := json.Unmarshal(file, &event); err != nil {
		fmt.Printf("❌ Невалидный JSON: %v\n", err)
		os.Exit(1)
	}

	// Добавляем timestamp если отсутствует
	if _, ok := event["timestamp"]; !ok {
		event["timestamp"] = time.Now()
		file, _ = json.Marshal(event)
	}

	url := getBaseURL() + "/api/v1/events"
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(file))
	if err != nil {
		fmt.Printf("❌ Ошибка отправки: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	
	if resp.StatusCode == http.StatusAccepted {
		fmt.Println("✅ Событие отправлено успешно")
		fmt.Printf("   Response: %s\n", string(body))
	} else {
		fmt.Printf("⚠️  Сервер вернул статус: %d\n", resp.StatusCode)
		fmt.Printf("   Response: %s\n", string(body))
	}
}

