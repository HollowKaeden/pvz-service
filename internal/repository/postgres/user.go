package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

// UserRepository хранит пользователей в таблице users.
type UserRepository struct {
	db *DB
}

// NewUserRepository создаёт репозиторий пользователей.
func NewUserRepository(db *DB) *UserRepository {
	return &UserRepository{db: db}
}

// Create сохраняет пользователя. Роль хранится ссылкой на справочник: id роли
// находится по названию в том же запросе. Если email уже занят, возвращает
// domain.ErrEmailTaken.
func (r *UserRepository) Create(ctx context.Context, u domain.User) error {
	tag, err := r.db.conn(ctx).Exec(ctx, `
		INSERT INTO users (id, email, password_hash, role_id)
		SELECT $1, $2, $3, r.id FROM roles r WHERE r.name = $4`,
		u.ID, u.Email, string(u.PasswordHash), u.Role,
	)
	// Занятость email проверяет сама база: отдельный SELECT перед вставкой не
	// защитил бы от двух одновременных регистраций с одним адресом.
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
		pgErr.Code == uniqueViolation && pgErr.ConstraintName == "users_email_key" {
		return domain.ErrEmailTaken
	}
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	// Сервис проверил роль, так что 0 строк значит, что справочник ролей в
	// базе разошёлся с кодом.
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("insert user: роли %q нет в справочнике", u.Role)
	}
	return nil
}

// ByEmail находит пользователя по email. Если его нет, возвращает
// domain.ErrUserNotFound.
func (r *UserRepository) ByEmail(ctx context.Context, email string) (domain.User, error) {
	var (
		u    domain.User
		hash string
	)
	err := r.db.conn(ctx).QueryRow(ctx, `
		SELECT u.id, u.email, u.password_hash, r.name
		FROM users u
		JOIN roles r ON r.id = u.role_id
		WHERE u.email = $1`,
		email,
	).Scan(&u.ID, &u.Email, &hash, &u.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrUserNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("select user: %w", err)
	}
	u.PasswordHash = []byte(hash)
	return u, nil
}
