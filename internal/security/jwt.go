package security

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog/log"
)

// Claims содержит данные JWT токена
type Claims struct {
	UserID   string   `json:"user_id"`
	Username string   `json:"username"`
	Roles    []string `json:"roles"`
	jwt.RegisteredClaims
}

// JWTManager управляет генерацией и валидацией JWT токенов
type JWTManager struct {
	secret     []byte
	expiration time.Duration
	issuer     string
}

// NewJWTManager создает новый JWT менеджер
func NewJWTManager(secret string, expirationHours int, issuer string) (*JWTManager, error) {
	if secret == "" {
		return nil, errors.New("JWT secret cannot be empty")
	}

	if len(secret) < 32 {
		log.Warn().Msg("JWT secret is too short, recommended minimum is 32 characters")
	}

	return &JWTManager{
		secret:     []byte(secret),
		expiration: time.Duration(expirationHours) * time.Hour,
		issuer:     issuer,
	}, nil
}

// GenerateToken генерирует новый JWT токен
func (jm *JWTManager) GenerateToken(userID, username string, roles []string) (string, error) {
	now := time.Now()

	claims := &Claims{
		UserID:   userID,
		Username: username,
		Roles:    roles,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(jm.expiration)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    jm.issuer,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(jm.secret)
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	log.Debug().
		Str("user_id", userID).
		Str("username", username).
		Strs("roles", roles).
		Msg("JWT token generated")

	return tokenString, nil
}

// ValidateToken валидирует JWT токен и возвращает claims
func (jm *JWTManager) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// Проверяем алгоритм подписи
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return jm.secret, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token claims")
	}

	return claims, nil
}

// RefreshToken обновляет токен (создает новый с теми же claims)
func (jm *JWTManager) RefreshToken(oldTokenString string) (string, error) {
	claims, err := jm.ValidateToken(oldTokenString)
	if err != nil {
		return "", fmt.Errorf("cannot refresh invalid token: %w", err)
	}

	// Генерируем новый токен с теми же данными
	return jm.GenerateToken(claims.UserID, claims.Username, claims.Roles)
}

// Middleware для HTTP серверов
type contextKey string

const claimsContextKey contextKey = "jwt_claims"

// HTTPMiddleware возвращает middleware для HTTP
func (jm *JWTManager) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Извлекаем токен из заголовка Authorization
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Authorization header required", http.StatusUnauthorized)
			return
		}

		// Ожидаем формат: "Bearer <token>"
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			http.Error(w, "Invalid authorization header format", http.StatusUnauthorized)
			return
		}

		tokenString := parts[1]

		// Валидируем токен
		claims, err := jm.ValidateToken(tokenString)
		if err != nil {
			log.Warn().Err(err).Msg("Invalid JWT token")
			http.Error(w, "Invalid or expired token", http.StatusUnauthorized)
			return
		}

		// Добавляем claims в контекст
		ctx := context.WithValue(r.Context(), claimsContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// OptionalHTTPMiddleware - опциональная аутентификация (не блокирует без токена)
func (jm *JWTManager) OptionalHTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			// Нет токена - пропускаем дальше без claims
			next.ServeHTTP(w, r)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) == 2 && parts[0] == "Bearer" {
			if claims, err := jm.ValidateToken(parts[1]); err == nil {
				ctx := context.WithValue(r.Context(), claimsContextKey, claims)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		// Невалидный токен - пропускаем без claims
		next.ServeHTTP(w, r)
	})
}

// GetClaimsFromContext извлекает claims из контекста
func GetClaimsFromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsContextKey).(*Claims)
	return claims, ok
}

// RequireRole проверяет наличие роли у пользователя
func RequireRole(requiredRole string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := GetClaimsFromContext(r.Context())
			if !ok {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			hasRole := false
			for _, role := range claims.Roles {
				if role == requiredRole {
					hasRole = true
					break
				}
			}

			if !hasRole {
				log.Warn().
					Str("user_id", claims.UserID).
					Str("required_role", requiredRole).
					Msg("Access denied: insufficient permissions")
				http.Error(w, "Forbidden: insufficient permissions", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireAnyRole проверяет наличие хотя бы одной из указанных ролей
func RequireAnyRole(requiredRoles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := GetClaimsFromContext(r.Context())
			if !ok {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			hasRequiredRole := false
			for _, userRole := range claims.Roles {
				for _, requiredRole := range requiredRoles {
					if userRole == requiredRole {
						hasRequiredRole = true
						break
					}
				}
				if hasRequiredRole {
					break
				}
			}

			if !hasRequiredRole {
				log.Warn().
					Str("user_id", claims.UserID).
					Strs("required_roles", requiredRoles).
					Msg("Access denied: insufficient permissions")
				http.Error(w, "Forbidden: insufficient permissions", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

