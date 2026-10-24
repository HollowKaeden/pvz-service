// Package auth выдаёт и проверяет JWT с ролью пользователя.
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

// ErrInvalidToken — токен не прошёл проверку: подпись, срок действия, формат
// или неизвестная роль. Подробности клиенту не нужны.
var ErrInvalidToken = errors.New("токен недействителен")

// Tokens подписывает и проверяет токены секретом из конфига (JWT_SECRET).
type Tokens struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

// NewTokens создаёт Tokens; ttl — срок жизни выданного токена.
func NewTokens(secret []byte, ttl time.Duration) *Tokens {
	return &Tokens{secret: secret, ttl: ttl, now: time.Now}
}

type claims struct {
	Role domain.Role `json:"role"`
	jwt.RegisteredClaims
}

// Issue выдаёт токен для роли.
func (t *Tokens) Issue(role domain.Role) (string, error) {
	now := t.now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(t.ttl)),
		},
	})
	signed, err := token.SignedString(t.secret)
	if err != nil {
		return "", fmt.Errorf("подпись токена: %w", err)
	}
	return signed, nil
}

// Parse проверяет токен и возвращает роль из него. Принимается только HS256
// (иначе токен с alg=none или другим алгоритмом мог бы пройти проверку),
// срок действия обязателен.
func (t *Tokens) Parse(token string) (domain.Role, error) {
	var c claims
	_, err := jwt.ParseWithClaims(token, &c,
		func(*jwt.Token) (any, error) { return t.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(t.now),
	)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	if !c.Role.Valid() {
		return "", fmt.Errorf("%w: роль %q", ErrInvalidToken, c.Role)
	}
	return c.Role, nil
}
