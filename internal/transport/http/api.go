package httptransport

import (
	"context"

	"github.com/HollowKaeden/pvz-service/internal/domain"
	"github.com/HollowKaeden/pvz-service/internal/transport/http/oapi"
)

// AuthService — вход в систему.
type AuthService interface {
	DummyLogin(ctx context.Context, role domain.Role) (string, error)
}

// API реализует ручки контракта (oapi.StrictServerInterface). Сгенерированный
// код разбирает запрос в DTO и сериализует ответ, здесь остаётся маппинг
// DTO ↔ domain и вызов сервисов.
//
// Ручки, логика которых ещё не написана, отвечают 501.
type API struct {
	auth AuthService
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

// PostPVZ — POST /pvz: заведение ПВЗ (moderator).
func (API) PostPVZ(context.Context, oapi.PostPVZRequestObject) (oapi.PostPVZResponseObject, error) {
	return nil, errNotImplemented
}

// GetPVZ — GET /pvz: ПВЗ с приёмками и товарами, фильтр по дате и пагинация.
func (API) GetPVZ(context.Context, oapi.GetPVZRequestObject) (oapi.GetPVZResponseObject, error) {
	return nil, errNotImplemented
}

// PostReceptions — POST /receptions: открытие приёмки (employee).
func (API) PostReceptions(context.Context, oapi.PostReceptionsRequestObject) (oapi.PostReceptionsResponseObject, error) {
	return nil, errNotImplemented
}

// PostPVZPVZIDCloseLastReception — POST /pvz/{pvzId}/close_last_reception: закрытие приёмки (employee).
func (API) PostPVZPVZIDCloseLastReception(
	context.Context, oapi.PostPVZPVZIDCloseLastReceptionRequestObject,
) (oapi.PostPVZPVZIDCloseLastReceptionResponseObject, error) {
	return nil, errNotImplemented
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
