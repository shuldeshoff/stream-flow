package websocket

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	"github.com/sul/streamflow/internal/cache"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // В production нужна более строгая проверка
	},
}

type WebSocketServer struct {
	cache        *cache.RedisCache
	clients      map[*Client]bool
	mu           sync.RWMutex
	register     chan *Client
	unregister   chan *Client
	broadcast    chan []byte
	statsUpdates chan map[string]interface{}
	done         chan struct{}
}

type Client struct {
	conn      *websocket.Conn
	send      chan []byte
	server    *WebSocketServer
	filters   map[string]string // Фильтры подписки
	mu        sync.RWMutex
}

func NewWebSocketServer(cache *cache.RedisCache) *WebSocketServer {
	ws := &WebSocketServer{
		cache:        cache,
		clients:      make(map[*Client]bool),
		register:     make(chan *Client),
		unregister:   make(chan *Client),
		broadcast:    make(chan []byte, 256),
		statsUpdates: make(chan map[string]interface{}, 100),
		done:         make(chan struct{}),
	}

	go ws.run()
	go ws.publishStats()

	return ws
}

func (ws *WebSocketServer) run() {
	for {
		select {
		case client := <-ws.register:
			ws.mu.Lock()
			ws.clients[client] = true
			ws.mu.Unlock()
			log.Debug().Msg("WebSocket client connected")

		case client := <-ws.unregister:
			ws.mu.Lock()
			if _, ok := ws.clients[client]; ok {
				delete(ws.clients, client)
				close(client.send)
			}
			ws.mu.Unlock()
			log.Debug().Msg("WebSocket client disconnected")

		case message := <-ws.broadcast:
			ws.mu.RLock()
			for client := range ws.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(ws.clients, client)
				}
			}
			ws.mu.RUnlock()

		case stats := <-ws.statsUpdates:
			ws.broadcastStats(stats)

		case <-ws.done:
			return
		}
	}
}

func (ws *WebSocketServer) publishStats() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if ws.cache != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
				
				// Получаем статистику за последнюю минуту
				stats, err := ws.cache.GetAllEventTypeStats(ctx, 1*time.Minute)
				cancel()

				if err == nil && len(stats) > 0 {
					ws.statsUpdates <- map[string]interface{}{
						"type":       "stats_update",
						"timestamp":  time.Now(),
						"type_stats": stats,
					}
				}
			}

		case <-ws.done:
			return
		}
	}
}

func (ws *WebSocketServer) broadcastStats(stats map[string]interface{}) {
	data, err := json.Marshal(stats)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal stats")
		return
	}

	ws.mu.RLock()
	defer ws.mu.RUnlock()

	for client := range ws.clients {
		select {
		case client.send <- data:
		default:
			// Канал заполнен, пропускаем
		}
	}
}

func (ws *WebSocketServer) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error().Err(err).Msg("WebSocket upgrade failed")
		return
	}

	client := &Client{
		conn:    conn,
		send:    make(chan []byte, 256),
		server:  ws,
		filters: make(map[string]string),
	}

	ws.register <- client

	// Запускаем горутины для чтения и записи
	go client.writePump()
	go client.readPump()

	// Отправляем приветственное сообщение
	welcome := map[string]interface{}{
		"type":    "connected",
		"message": "StreamFlow WebSocket connected",
		"time":    time.Now(),
	}
	data, _ := json.Marshal(welcome)
	client.send <- data
}

func (c *Client) readPump() {
	defer func() {
		c.server.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Error().Err(err).Msg("WebSocket read error")
			}
			break
		}

		// Обрабатываем сообщение от клиента
		c.handleMessage(message)
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Добавляем дополнительные сообщения из очереди
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write([]byte{'\n'})
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Client) handleMessage(message []byte) {
	var msg map[string]interface{}
	if err := json.Unmarshal(message, &msg); err != nil {
		log.Warn().Err(err).Msg("Invalid WebSocket message")
		return
	}

	msgType, ok := msg["type"].(string)
	if !ok {
		return
	}

	switch msgType {
	case "subscribe":
		// Подписка на определенные типы событий
		if eventType, ok := msg["event_type"].(string); ok {
			c.mu.Lock()
			c.filters["event_type"] = eventType
			c.mu.Unlock()
			
			response := map[string]interface{}{
				"type":       "subscribed",
				"event_type": eventType,
			}
			data, _ := json.Marshal(response)
			c.send <- data
		}

	case "unsubscribe":
		c.mu.Lock()
		delete(c.filters, "event_type")
		c.mu.Unlock()

		response := map[string]interface{}{
			"type":    "unsubscribed",
			"message": "Filters cleared",
		}
		data, _ := json.Marshal(response)
		c.send <- data

	case "ping":
		response := map[string]interface{}{
			"type": "pong",
			"time": time.Now(),
		}
		data, _ := json.Marshal(response)
		c.send <- data

	case "get_stats":
		// Запрос текущей статистики
		if c.server.cache != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			stats, err := c.server.cache.GetAllEventTypeStats(ctx, 1*time.Minute)
			if err == nil {
				response := map[string]interface{}{
					"type":       "stats",
					"type_stats": stats,
					"timestamp":  time.Now(),
				}
				data, _ := json.Marshal(response)
				c.send <- data
			}
		}
	}
}

func (ws *WebSocketServer) BroadcastEvent(eventType, source string, data map[string]interface{}) {
	message := map[string]interface{}{
		"type":       "event",
		"event_type": eventType,
		"source":     source,
		"data":       data,
		"timestamp":  time.Now(),
	}

	jsonData, err := json.Marshal(message)
	if err != nil {
		return
	}

	select {
	case ws.broadcast <- jsonData:
	default:
		// Broadcast канал заполнен, пропускаем
	}
}

func (ws *WebSocketServer) Stop() {
	close(ws.done)
	
	ws.mu.Lock()
	for client := range ws.clients {
		client.conn.Close()
	}
	ws.mu.Unlock()
}

func (ws *WebSocketServer) GetStats() map[string]interface{} {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	return map[string]interface{}{
		"connected_clients": len(ws.clients),
	}
}

