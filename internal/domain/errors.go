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

	// ErrPVZNotFound — ПВЗ с таким id нет.
	ErrPVZNotFound = errors.New("ПВЗ не найден")

	// ErrReceptionAlreadyOpen — на ПВЗ уже есть незакрытая приёмка, вторую
	// открыть нельзя.
	ErrReceptionAlreadyOpen = errors.New("на ПВЗ уже есть незакрытая приёмка")

	// ErrNoOpenReception — на ПВЗ нет открытой приёмки: её не открывали или
	// уже закрыли.
	ErrNoOpenReception = errors.New("на ПВЗ нет открытой приёмки")

	// ErrReceptionEmpty — в открытой приёмке нет товаров, закрывать её нельзя.
	ErrReceptionEmpty = errors.New("нельзя закрыть приёмку без товаров")

	// ErrProductTypeNotAllowed — такого типа товара нет в справочнике.
	ErrProductTypeNotAllowed = errors.New("недопустимый тип товара")

	// ErrNoProductsToDelete — в открытой приёмке нет товаров, удалять нечего.
	ErrNoProductsToDelete = errors.New("в приёмке нет товаров для удаления")

	// ErrInvalidListParams — неверные параметры списка ПВЗ: страница, её
	// размер или период.
	ErrInvalidListParams = errors.New("неверные параметры списка")
)
