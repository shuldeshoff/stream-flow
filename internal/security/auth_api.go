package security

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// AuthAPI предоставляет endpoints для аутентификации
type AuthAPI struct {
	jwtManager *JWTManager
	// В production здесь должна быть интеграция с БД пользователей
	// Для демо используем простую in-memory базу
	users map[string]User
}

// User представляет пользователя системы
type User struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Password string   `json:"-"` // Хеш пароля, не отдаем в JSON
	Roles    []string `json:"roles"`
	Email    string   `json:"email"`
	Active   bool     `json:"active"`
}

// LoginRequest запрос на вход
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse ответ на вход
type LoginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      UserInfo  `json:"user"`
}

// UserInfo информация о пользователе
type UserInfo struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Email    string   `json:"email"`
	Roles    []string `json:"roles"`
}

// RefreshRequest запрос на обновление токена
type RefreshRequest struct {
	Token string `json:"token"`
}

// NewAuthAPI создает новый Auth API
func NewAuthAPI(jwtManager *JWTManager) *AuthAPI {
	api := &AuthAPI{
		jwtManager: jwtManager,
		users:      make(map[string]User),
	}

	// Создаем демо пользователей
	api.createDemoUsers()

	return api
}

// createDemoUsers создает тестовых пользователей
func (a *AuthAPI) createDemoUsers() {
	// Admin user
	a.users["admin"] = User{
		ID:       "user-admin-001",
		Username: "admin",
		Password: "admin123", // В production должен быть bcrypt хеш
		Email:    "admin@streamflow.local",
		Roles:    []string{"admin", "user"},
		Active:   true,
	}

	// Regular user
	a.users["user"] = User{
		ID:       "user-regular-001",
		Username: "user",
		Password: "user123",
		Email:    "user@streamflow.local",
		Roles:    []string{"user"},
		Active:   true,
	}

	// Banking API user
	a.users["banking"] = User{
		ID:       "user-banking-001",
		Username: "banking",
		Password: "banking123",
		Email:    "banking@streamflow.local",
		Roles:    []string{"banking", "user"},
		Active:   true,
	}

	log.Info().
		Int("count", len(a.users)).
		Msg("Demo users created (admin/admin123, user/user123, banking/banking123)")
}

// RegisterRoutes регистрирует auth endpoints
func (a *AuthAPI) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/auth/login", a.handleLogin)
	mux.HandleFunc("/api/auth/refresh", a.handleRefresh)
	mux.HandleFunc("/api/auth/me", a.handleMe)

	log.Info().Msg("Auth API routes registered")
}

// handleLogin обрабатывает вход пользователя
func (a *AuthAPI) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Находим пользователя
	user, ok := a.users[req.Username]
	if !ok {
		log.Warn().Str("username", req.Username).Msg("Login attempt with unknown username")
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	// Проверяем пароль (в production используйте bcrypt.CompareHashAndPassword)
	if user.Password != req.Password {
		log.Warn().Str("username", req.Username).Msg("Login attempt with wrong password")
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	// Проверяем, активен ли пользователь
	if !user.Active {
		log.Warn().Str("username", req.Username).Msg("Login attempt for inactive user")
		http.Error(w, "User account is disabled", http.StatusForbidden)
		return
	}

	// Генерируем JWT токен
	token, err := a.jwtManager.GenerateToken(user.ID, user.Username, user.Roles)
	if err != nil {
		log.Error().Err(err).Msg("Failed to generate JWT token")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Вычисляем время истечения
	expiresAt := time.Now().Add(24 * time.Hour) // TODO: взять из config

	response := LoginResponse{
		Token:     token,
		ExpiresAt: expiresAt,
		User: UserInfo{
			ID:       user.ID,
			Username: user.Username,
			Email:    user.Email,
			Roles:    user.Roles,
		},
	}

	log.Info().
		Str("user_id", user.ID).
		Str("username", user.Username).
		Strs("roles", user.Roles).
		Msg("User logged in successfully")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleRefresh обновляет токен
func (a *AuthAPI) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Обновляем токен
	newToken, err := a.jwtManager.RefreshToken(req.Token)
	if err != nil {
		log.Warn().Err(err).Msg("Token refresh failed")
		http.Error(w, "Invalid or expired token", http.StatusUnauthorized)
		return
	}

	expiresAt := time.Now().Add(24 * time.Hour)

	response := map[string]interface{}{
		"token":      newToken,
		"expires_at": expiresAt,
	}

	log.Debug().Msg("Token refreshed successfully")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleMe возвращает информацию о текущем пользователе
func (a *AuthAPI) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Извлекаем claims из контекста (должен быть установлен JWT middleware)
	claims, ok := GetClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Находим пользователя
	var user *User
	for _, u := range a.users {
		if u.ID == claims.UserID {
			user = &u
			break
		}
	}

	if user == nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	userInfo := UserInfo{
		ID:       user.ID,
		Username: user.Username,
		Email:    user.Email,
		Roles:    user.Roles,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(userInfo)
}

