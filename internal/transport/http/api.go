package httptransport

import (
	"context"

	"github.com/google/uuid"

	"github.com/HollowKaeden/pvz-service/internal/domain"
	"github.com/HollowKaeden/pvz-service/internal/transport/http/oapi"
)

// AuthService — вход в систему.
type AuthService interface {
	DummyLogin(ctx context.Context, role domain.Role) (string, error)
}

// PVZService — заведение ПВЗ.
type PVZService interface {
	Create(ctx context.Context, city string) (domain.PVZ, error)
}

// ReceptionService — открытие и закрытие приёмок.
type ReceptionService interface {
	Open(ctx context.Context, pvzID uuid.UUID) (domain.Reception, error)
	Close(ctx context.Context, pvzID uuid.UUID) (domain.Reception, error)
}

// API реализует ручки контракта (oapi.StrictServerInterface). Сгенерированный
// код разбирает запрос в DTO и сериализует ответ, здесь остаётся маппинг
// DTO ↔ domain и вызов сервисов.
//
// Ручки, логика которых ещё не написана, отвечают 501.
type API struct {
	auth       AuthService
	pvz        PVZService
	receptions ReceptionService
}

var _ oapi.StrictServerInterface = API{}

// PostDummyLogin — POST /dummyLogin: тестовый токен для выбранной роли.
func (a API) PostDummyLogin(
	ctx context.Context, req oapi.PostDummyLoginRequestObject,
) (oapi.PostDummyLoginResponseObject, error) {
	token, err := a.auth.DummyLogin(ctx, domain.Role(req.Body.Role))
	if err != nil {
		return nil, err
	}
	return oapi.PostDummyLogin200JSONResponse(token), nil
}

// PostRegister — POST /register: регистрация по email и паролю.
func (API) PostRegister(context.Context, oapi.PostRegisterRequestObject) (oapi.PostRegisterResponseObject, error) {
	return nil, errNotImplemented
}

// PostLogin — POST /login: вход по email и паролю.
func (API) PostLogin(context.Context, oapi.PostLoginRequestObject) (oapi.PostLoginResponseObject, error) {
	return nil, errNotImplemented
}

// PostPVZ — POST /pvz: заведение ПВЗ (moderator). id и registrationDate из
// тела не используются: их назначает сервис.
func (a API) PostPVZ(ctx context.Context, req oapi.PostPVZRequestObject) (oapi.PostPVZResponseObject, error) {
	pvz, err := a.pvz.Create(ctx, string(req.Body.City))
	if err != nil {
		return nil, err
	}
	return oapi.PostPVZ201JSONResponse(toPVZ(pvz)), nil
}

// GetPVZ — GET /pvz: ПВЗ с приёмками и товарами, фильтр по дате и пагинация.
func (API) GetPVZ(context.Context, oapi.GetPVZRequestObject) (oapi.GetPVZResponseObject, error) {
	return nil, errNotImplemented
}

// PostReceptions — POST /receptions: открытие приёмки (employee).
func (a API) PostReceptions(
	ctx context.Context, req oapi.PostReceptionsRequestObject,
) (oapi.PostReceptionsResponseObject, error) {
	rec, err := a.receptions.Open(ctx, req.Body.PVZID)
	if err != nil {
		return nil, err
	}
	return oapi.PostReceptions201JSONResponse(toReception(rec)), nil
}

// PostPVZPVZIDCloseLastReception — POST /pvz/{pvzId}/close_last_reception: закрытие приёмки (employee).
func (a API) PostPVZPVZIDCloseLastReception(
	ctx context.Context, req oapi.PostPVZPVZIDCloseLastReceptionRequestObject,
) (oapi.PostPVZPVZIDCloseLastReceptionResponseObject, error) {
	rec, err := a.receptions.Close(ctx, req.PVZID)
	if err != nil {
		return nil, err
	}
	return oapi.PostPVZPVZIDCloseLastReception200JSONResponse(toReception(rec)), nil
}

// PostProducts — POST /products: товар в открытую приёмку (employee).
func (API) PostProducts(context.Context, oapi.PostProductsRequestObject) (oapi.PostProductsResponseObject, error) {
	return nil, errNotImplemented
}

// PostPVZPVZIDDeleteLastProduct — POST /pvz/{pvzId}/delete_last_product: удаление последнего товара, LIFO (employee).
func (API) PostPVZPVZIDDeleteLastProduct(
	context.Context, oapi.PostPVZPVZIDDeleteLastProductRequestObject,
) (oapi.PostPVZPVZIDDeleteLastProductResponseObject, error) {
	return nil, errNotImplemented
}

// toPVZ переводит ПВЗ из домена в DTO контракта.
func toPVZ(p domain.PVZ) oapi.PVZ {
	return oapi.PVZ{
		ID:               &p.ID,
		RegistrationDate: &p.RegistrationDate,
		City:             oapi.PVZCity(p.City),
	}
}

// toReception переводит приёмку из домена в DTO контракта.
func toReception(r domain.Reception) oapi.Reception {
	return oapi.Reception{
		ID:       &r.ID,
		DateTime: r.DateTime,
		PVZID:    r.PVZID,
		Status:   oapi.ReceptionStatus(r.Status),
	}
}
