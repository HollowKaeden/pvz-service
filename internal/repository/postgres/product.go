package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

// ProductRepository хранит товары в таблице products.
type ProductRepository struct {
	db *DB
}

// NewProductRepository создаёт репозиторий товаров.
func NewProductRepository(db *DB) *ProductRepository {
	return &ProductRepository{db: db}
}

// Create сохраняет товар. Тип хранится ссылкой на справочник: id типа
// находится по названию в том же запросе. Порядковый номер seq назначает база.
func (r *ProductRepository) Create(ctx context.Context, p domain.Product) error {
	tag, err := r.db.conn(ctx).Exec(ctx, `
		INSERT INTO products (id, reception_id, type_id, date_time)
		SELECT $1, $2, t.id, $4 FROM product_types t WHERE t.name = $3`,
		p.ID, p.ReceptionID, p.Type, p.DateTime,
	)
	if err != nil {
		return fmt.Errorf("insert product: %w", err)
	}
	// Сервис проверил тип по справочнику, так что 0 строк значит, что
	// справочник в базе и в памяти разошёлся.
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("insert product: типа %q нет в справочнике", p.Type)
	}
	return nil
}

// DeleteLast удаляет товар приёмки с наибольшим порядковым номером. Если
// товаров нет, возвращает domain.ErrNoProductsToDelete.
func (r *ProductRepository) DeleteLast(ctx context.Context, receptionID uuid.UUID) error {
	// Последний товар находится по индексу (reception_id, seq).
	tag, err := r.db.conn(ctx).Exec(ctx, `
		DELETE FROM products
		WHERE id = (
			SELECT id FROM products
			WHERE reception_id = $1
			ORDER BY seq DESC
			LIMIT 1
		)`,
		receptionID,
	)
	if err != nil {
		return fmt.Errorf("delete last product: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNoProductsToDelete
	}
	return nil
}

// ProductTypeRepository читает справочник типов товаров.
type ProductTypeRepository struct {
	db *DB
}

// NewProductTypeRepository создаёт репозиторий справочника типов товаров.
func NewProductTypeRepository(db *DB) *ProductTypeRepository {
	return &ProductTypeRepository{db: db}
}

// List возвращает названия типов в порядке справочника.
func (r *ProductTypeRepository) List(ctx context.Context) ([]string, error) {
	rows, err := r.db.conn(ctx).Query(ctx, `SELECT name FROM product_types ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("select product types: %w", err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("select product types: %w", err)
	}
	return names, nil
}
