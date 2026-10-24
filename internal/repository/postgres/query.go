package postgres

import (
	sq "github.com/Masterminds/squirrel"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

// psql — построитель запросов squirrel с плейсхолдерами Postgres ($1, $2...).
// squirrel нужен только там, где условия WHERE зависят от параметров запроса,
// остальные запросы написаны обычным SQL.
func psql() sq.StatementBuilderType {
	return sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
}

// inPeriod — условие «дата в column попадает в период», границы включительно.
// Пустой период не ограничивает ничего, поэтому вызывать только для
// period.IsSet().
func inPeriod(column string, period domain.Period) sq.And {
	var conds sq.And
	if period.Start != nil {
		conds = append(conds, sq.GtOrEq{column: *period.Start})
	}
	if period.End != nil {
		conds = append(conds, sq.LtOrEq{column: *period.End})
	}
	return conds
}
