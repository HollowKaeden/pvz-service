package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

var testSecret = []byte("test-secret-0123456789abcdef-0123")

func TestIssueAndParse(t *testing.T) {
	tokens := NewTokens(testSecret, time.Hour)

	for _, role := range []domain.Role{domain.RoleEmployee, domain.RoleModerator} {
		t.Run(string(role), func(t *testing.T) {
			token, err := tokens.Issue(role)
			require.NoError(t, err)

			got, err := tokens.Parse(token)
			require.NoError(t, err)
			assert.Equal(t, role, got)
		})
	}
}

func TestParseRejects(t *testing.T) {
	start := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	tokens := NewTokens(testSecret, time.Hour)
	tokens.now = func() time.Time { return start }

	valid, err := tokens.Issue(domain.RoleModerator)
	require.NoError(t, err)

	sign := func(method jwt.SigningMethod, key any, c jwt.Claims) string {
		t.Helper()
		s, err := jwt.NewWithClaims(method, c).SignedString(key)
		require.NoError(t, err)
		return s
	}
	exp := jwt.NewNumericDate(start.Add(time.Hour))

	tests := []struct {
		name  string
		token string
		now   time.Time
	}{
		{name: "срок истёк", token: valid, now: start.Add(time.Hour + time.Second)},
		{name: "чужой секрет", token: sign(jwt.SigningMethodHS256, []byte("another-secret-0123456789abcdef-0"),
			claims{Role: domain.RoleModerator, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: exp}})},
		{name: "другой алгоритм", token: sign(jwt.SigningMethodHS512, testSecret,
			claims{Role: domain.RoleModerator, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: exp}})},
		{name: "alg none", token: sign(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType,
			claims{Role: domain.RoleModerator, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: exp}})},
		{name: "без срока действия", token: sign(jwt.SigningMethodHS256, testSecret,
			claims{Role: domain.RoleModerator})},
		{name: "неизвестная роль", token: sign(jwt.SigningMethodHS256, testSecret,
			claims{Role: "admin", RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: exp}})},
		{name: "подпись изменена", token: valid[:len(valid)-2] + flip(valid[len(valid)-2:])},
		{name: "не JWT", token: "not-a-token"},
		{name: "пустой", token: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := tt.now
			if now.IsZero() {
				now = start
			}
			tokens.now = func() time.Time { return now }

			role, err := tokens.Parse(tt.token)

			require.ErrorIs(t, err, ErrInvalidToken)
			assert.Empty(t, role)
		})
	}
}

// flip меняет символы base64url так, чтобы подпись стала другой.
func flip(s string) string {
	return strings.Map(func(r rune) rune {
		if r == 'A' {
			return 'B'
		}
		return 'A'
	}, s)
}
