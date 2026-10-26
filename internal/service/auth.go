package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

// TokenIssuer выдаёт токен доступа для роли.
type TokenIssuer interface {
	Issue(role domain.Role) (string, error)
}

// UserRepository хранит пользователей.
type UserRepository interface {
	// Create сохраняет пользователя. Если email уже занят, возвращает
	// domain.ErrEmailTaken.
	Create(ctx context.Context, u domain.User) error
	// ByEmail находит пользователя по email. Если его нет, возвращает
	// domain.ErrUserNotFound.
	ByEmail(ctx context.Context, email string) (domain.User, error)
}

// Требования к паролю. Верхняя граница в байтах: bcrypt учитывает только
// первые 72 байта, остальное он бы молча отбросил. Кириллическая буква
// занимает 2 байта.
const (
	minPasswordRunes = 8
	maxPasswordBytes = 72
)

// Auth — регистрация и вход: тестовый вход по роли (/dummyLogin) и вход по
// email и паролю.
type Auth struct {
	tokens TokenIssuer
	users  UserRepository

	// hashCost — стоимость bcrypt: на 1 vCPU сервера один хеш по умолчанию
	// считается десятки миллисекунд. Тесты ставят минимальную.
	hashCost int
	// dummyHash — хеш, с которым сравнивается пароль, если пользователя нет.
	// Так ответ «нет такого email» занимает столько же времени, сколько
	// «неверный пароль», и по времени ответа нельзя узнать, кто
	// зарегистрирован. Считается один раз, при первом таком входе.
	dummyHash func() []byte
	newID     func() (uuid.UUID, error)
}

// NewAuth создаёт сервис входа.
func NewAuth(tokens TokenIssuer, users UserRepository) *Auth {
	a := &Auth{tokens: tokens, users: users, hashCost: bcrypt.DefaultCost, newID: uuid.NewV7}
	a.dummyHash = sync.OnceValue(func() []byte {
		// Ошибки быть не может: пароль короткий, стоимость допустимая.
		hash, _ := bcrypt.GenerateFromPassword([]byte("пароль, которого ни у кого нет"), a.hashCost)
		return hash
	})
	return a
}

// DummyLogin выдаёт тестовый токен с выбранной ролью, без пользователя и
// пароля (ручка /dummyLogin из задания).
func (a *Auth) DummyLogin(_ context.Context, role domain.Role) (string, error) {
	if err := checkRole(role); err != nil {
		return "", err
	}
	token, err := a.tokens.Issue(role)
	if err != nil {
		return "", fmt.Errorf("dummy login: %w", err)
	}
	return token, nil
}

// Register заводит пользователя с ролью. Формат email проверен при разборе
// запроса; здесь он приводится к одному виду, чтобы Ivan@Example.com и
// ivan@example.com считались одним адресом.
func (a *Auth) Register(ctx context.Context, email, password string, role domain.Role) (domain.User, error) {
	if err := checkRole(role); err != nil {
		return domain.User{}, err
	}
	if err := checkPassword(password); err != nil {
		return domain.User{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), a.hashCost)
	if err != nil {
		return domain.User{}, fmt.Errorf("хеш пароля: %w", err)
	}
	id, err := a.newID()
	if err != nil {
		return domain.User{}, fmt.Errorf("id пользователя: %w", err)
	}

	user := domain.User{ID: id, Email: normalizeEmail(email), Role: role, PasswordHash: hash}
	if err := a.users.Create(ctx, user); err != nil {
		if errors.Is(err, domain.ErrEmailTaken) {
			return domain.User{}, err
		}
		return domain.User{}, fmt.Errorf("создание пользователя: %w", err)
	}
	return user, nil
}

// Login проверяет email и пароль и выдаёт токен с ролью пользователя. На
// неизвестный email и на неверный пароль ответ один и тот же.
func (a *Auth) Login(ctx context.Context, email, password string) (string, error) {
	user, err := a.users.ByEmail(ctx, normalizeEmail(email))
	if errors.Is(err, domain.ErrUserNotFound) {
		_ = bcrypt.CompareHashAndPassword(a.dummyHash(), []byte(password))
		return "", domain.ErrInvalidCredentials
	}
	if err != nil {
		return "", fmt.Errorf("поиск пользователя: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(password)); err != nil {
		return "", domain.ErrInvalidCredentials
	}

	token, err := a.tokens.Issue(user.Role)
	if err != nil {
		return "", fmt.Errorf("login: %w", err)
	}
	return token, nil
}

func checkRole(role domain.Role) error {
	if !role.Valid() {
		return fmt.Errorf("%w %q: доступны %s и %s",
			domain.ErrInvalidRole, role, domain.RoleEmployee, domain.RoleModerator)
	}
	return nil
}

func checkPassword(password string) error {
	if utf8.RuneCountInString(password) < minPasswordRunes {
		return fmt.Errorf("%w: нужно не меньше %d символов", domain.ErrInvalidPassword, minPasswordRunes)
	}
	if len(password) > maxPasswordBytes {
		return fmt.Errorf("%w: не длиннее %d байт (латинская буква занимает 1 байт, кириллическая 2)",
			domain.ErrInvalidPassword, maxPasswordBytes)
	}
	return nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
