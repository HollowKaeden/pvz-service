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

// PVZService — заведение ПВЗ и список ПВЗ.
type PVZService interface {
	Create(ctx context.Context, city string) (domain.PVZ, error)
	List(ctx context.Context, period domain.Period, page, limit int) ([]domain.PVZWithReceptions, error)
}

// Значения по умолчанию для GET /pvz из контракта.
const (
	defaultPage  = 1
	defaultLimit = 10
)

// ReceptionService — открытие и закрытие приёмок.
type ReceptionService interface {
	Open(ctx context.Context, pvzID uuid.UUID) (domain.Reception, error)
	Close(ctx context.Context, pvzID uuid.UUID) (domain.Reception, error)
}

// ProductService — товары в открытой приёмке.
type ProductService interface {
	Add(ctx context.Context, pvzID uuid.UUID, productType string) (domain.Product, error)
	DeleteLast(ctx context.Context, pvzID uuid.UUID) error
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
	products   ProductService
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
//
// Проверка значений (диапазон limit, порядок дат) в сервисе: это правила
// списка, а не формат запроса.
func (a API) GetPVZ(ctx context.Context, req oapi.GetPVZRequestObject) (oapi.GetPVZResponseObject, error) {
	page, limit := defaultPage, defaultLimit
	if req.Params.Page != nil {
		page = *req.Params.Page
	}
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	period := domain.Period{Start: req.Params.StartDate, End: req.Params.EndDate}

	list, err := a.pvz.List(ctx, period, page, limit)
	if err != nil {
		return nil, err
	}
	resp := make(oapi.GetPVZ200JSONResponse, len(list))
	for i, item := range list {
		resp[i] = toPVZWithReceptions(item)
	}
	return resp, nil
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
func (a API) PostProducts(ctx context.Context, req oapi.PostProductsRequestObject) (oapi.PostProductsResponseObject, error) {
	p, err := a.products.Add(ctx, req.Body.PVZID, string(req.Body.Type))
	if err != nil {
		return nil, err
	}
	return oapi.PostProducts201JSONResponse(toProduct(p)), nil
}

// PostPVZPVZIDDeleteLastProduct — POST /pvz/{pvzId}/delete_last_product: удаление последнего товара, LIFO (employee).
func (a API) PostPVZPVZIDDeleteLastProduct(
	ctx context.Context, req oapi.PostPVZPVZIDDeleteLastProductRequestObject,
) (oapi.PostPVZPVZIDDeleteLastProductResponseObject, error) {
	if err := a.products.DeleteLast(ctx, req.PVZID); err != nil {
		return nil, err
	}
	return oapi.PostPVZPVZIDDeleteLastProduct200Response{}, nil
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

// toProduct переводит товар из домена в DTO контракта.
func toProduct(p domain.Product) oapi.Product {
	return oapi.Product{
		ID:          &p.ID,
		DateTime:    &p.DateTime,
		ReceptionID: p.ReceptionID,
		Type:        oapi.ProductType(p.Type),
	}
}

// toPVZWithReceptions переводит элемент списка ПВЗ из домена в DTO контракта.
// Пустые списки приёмок и товаров уходят как [], а не пропадают из ответа.
func toPVZWithReceptions(item domain.PVZWithReceptions) oapi.PVZWithReceptions {
	receptions := make([]oapi.ReceptionWithProducts, len(item.Receptions))
	for i, r := range item.Receptions {
		products := make([]oapi.Product, len(r.Products))
		for j, p := range r.Products {
			products[j] = toProduct(p)
		}
		reception := toReception(r.Reception)
		receptions[i] = oapi.ReceptionWithProducts{Reception: &reception, Products: &products}
	}
	pvz := toPVZ(item.PVZ)
	return oapi.PVZWithReceptions{PVZ: &pvz, Receptions: &receptions}
}
