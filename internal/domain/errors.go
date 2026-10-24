package domain

import "errors"

// Ошибки бизнес-правил. Сервисы возвращают их (иногда с уточнением через
// fmt.Errorf("%w ...")), а transport/http переводит в HTTP-код и показывает
// текст клиенту. Любая другая ошибка считается внутренней, и её текст наружу
// не уходит.
var (
	// ErrInvalidRole — роли нет среди employee и moderator.
	ErrInvalidRole = errors.New("недопустимая роль")

	// ErrCityNotAllowed — в этом городе ПВЗ открывать нельзя: его нет в
	// справочнике городов.
	ErrCityNotAllowed = errors.New("город недоступен для открытия ПВЗ")
)
