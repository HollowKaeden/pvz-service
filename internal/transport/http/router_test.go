package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/api"
	"github.com/HollowKaeden/pvz-service/internal/auth"
	"github.com/HollowKaeden/pvz-service/internal/domain"
	"github.com/HollowKaeden/pvz-service/internal/service"
)

// fakeDB — Pinger для тестов: возвращает заданную ошибку.
type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

// testTokens — настоящие JWT с тестовым секретом: роутер проверяется вместе
// с выдачей и проверкой токенов.
var testTokens = auth.NewTokens([]byte("test-secret-0123456789abcdef-0123"), time.Hour)

// fakePVZ — PVZService для тестов HTTP-слоя: заводит ПВЗ в Москве, на
// остальные города отвечает ошибкой бизнес-правила, на city "boom" — внутренней.
type fakePVZ struct{}

var testPVZ = domain.PVZ{
	ID:               uuid.MustParse("0192f1a4-7b3c-7d2e-8f00-000000000001"),
	RegistrationDate: time.Date(2026, 10, 24, 12, 4, 5, 123456000, time.UTC),
	City:             "Москва",
}

func (fakePVZ) Create(_ context.Context, city string) (domain.PVZ, error) {
	switch city {
	case "Москва":
		return testPVZ, nil
	case "boom":
		return domain.PVZ{}, errors.New("pq: connection to 10.0.0.5 refused")
	default:
		return domain.PVZ{}, fmt.Errorf("%w: %q", domain.ErrCityNotAllowed, city)
	}
}

// List отдаёт один ПВЗ с одной приёмкой без товаров. Параметры запоминает
// listSpy в тесте GET /pvz.
func (fakePVZ) List(context.Context, domain.Period, int, int) ([]domain.PVZWithReceptions, error) {
	return []domain.PVZWithReceptions{{
		PVZ: testPVZ,
		Receptions: []domain.ReceptionWithProducts{{
			Reception: testReception(testPVZ.ID, domain.ReceptionInProgress),
			Products:  []domain.Product{},
		}},
	}}, nil
}

// listSpy — PVZService, который запоминает параметры списка.
type listSpy struct {
	fakePVZ
	period      domain.Period
	page, limit int
	err         error
}

func (s *listSpy) List(ctx context.Context, period domain.Period, page, limit int) ([]domain.PVZWithReceptions, error) {
	s.period, s.page, s.limit = period, page, limit
	if s.err != nil {
		return nil, s.err
	}
	return s.fakePVZ.List(ctx, period, page, limit)
}

// fakeReceptions — ReceptionService для тестов HTTP-слоя: ведёт себя по pvzId
// из констант ниже.
type fakeReceptions struct{}

var (
	pvzWithReception = uuid.MustParse("6f1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d") // открыть нельзя, закрыть можно
	pvzEmpty         = uuid.MustParse("6f1b2c3d-4e5f-4a6b-8c7d-000000000002") // открыть можно, закрыть нечего
	pvzMissing       = uuid.MustParse("6f1b2c3d-4e5f-4a6b-8c7d-000000000003") // такого ПВЗ нет
)

func testReception(pvzID uuid.UUID, status domain.ReceptionStatus) domain.Reception {
	return domain.Reception{
		ID:       uuid.MustParse("0192f1a4-7b3c-7d2e-8f00-0000000000a1"),
		PVZID:    pvzID,
		DateTime: time.Date(2026, 10, 24, 12, 4, 5, 123456000, time.UTC),
		Status:   status,
	}
}

func (fakeReceptions) Open(_ context.Context, pvzID uuid.UUID) (domain.Reception, error) {
	switch pvzID {
	case pvzEmpty:
		return testReception(pvzID, domain.ReceptionInProgress), nil
	case pvzWithReception:
		return domain.Reception{}, domain.ErrReceptionAlreadyOpen
	default:
		return domain.Reception{}, domain.ErrPVZNotFound
	}
}

func (fakeReceptions) Close(_ context.Context, pvzID uuid.UUID) (domain.Reception, error) {
	switch pvzID {
	case pvzWithReception:
		return testReception(pvzID, domain.ReceptionClosed), nil
	case pvzEmpty:
		return domain.Reception{}, domain.ErrNoOpenReception
	default:
		return domain.Reception{}, domain.ErrPVZNotFound
	}
}

// fakeProducts — ProductService для тестов HTTP-слоя: в pvzWithReception есть
// открытая приёмка с товарами, у pvzEmpty открытой приёмки нет.
type fakeProducts struct{}

func (fakeProducts) Add(_ context.Context, pvzID uuid.UUID, productType string) (domain.Product, error) {
	switch {
	case productType != "обувь":
		return domain.Product{}, fmt.Errorf("%w: %q", domain.ErrProductTypeNotAllowed, productType)
	case pvzID == pvzWithReception:
		return domain.Product{
			ID:          uuid.MustParse("0192f1a4-7b3c-7d2e-8f00-0000000000b1"),
			ReceptionID: uuid.MustParse("0192f1a4-7b3c-7d2e-8f00-0000000000a1"),
			DateTime:    time.Date(2026, 10, 24, 12, 4, 5, 123456000, time.UTC),
			Type:        productType,
		}, nil
	case pvzID == pvzEmpty:
		return domain.Product{}, domain.ErrNoOpenReception
	default:
		return domain.Product{}, domain.ErrPVZNotFound
	}
}

func (fakeProducts) DeleteLast(_ context.Context, pvzID uuid.UUID) error {
	switch pvzID {
	case pvzWithReception:
		return nil
	case pvzEmpty:
		return domain.ErrNoProductsToDelete
	default:
		return domain.ErrPVZNotFound
	}
}

func newRouterWithDB(db Pinger) http.Handler {
	return NewRouter(slog.New(slog.DiscardHandler), Deps{
		DB:         db,
		Tokens:     testTokens,
		Auth:       service.NewAuth(testTokens),
		PVZ:        fakePVZ{},
		Receptions: fakeReceptions{},
		Products:   fakeProducts{},
	})
}

func newTestRouter() http.Handler {
	return newRouterWithDB(fakeDB{})
}

func tokenFor(t *testing.T, role domain.Role) string {
	t.Helper()
	token, err := testTokens.Issue(role)
	require.NoError(t, err)
	return token
}

func TestHealth(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		db         fakeDB
		wantStatus int
		wantBody   string
	}{
		{name: "БД отвечает", method: http.MethodGet, wantStatus: http.StatusOK, wantBody: `{"status":"ok"}`},
		{
			name:       "БД недоступна",
			method:     http.MethodGet,
			db:         fakeDB{err: errors.New("connection refused")},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `{"status":"unavailable"}`,
		},
		{name: "POST не разрешён", method: http.MethodPost, wantStatus: http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newRouterWithDB(tt.db)
			rec := serve(h, tt.method, "/health", "", "")

			assert.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantBody != "" {
				assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
				assert.JSONEq(t, tt.wantBody, rec.Body.String())
			}
		})
	}
}

// Ручки, логика которых ещё не написана, отвечают 501 с телом ошибки из
// контракта. Обе открытые, токен не нужен.
func TestContractRoutesNotImplemented(t *testing.T) {
	h := newTestRouter()

	tests := []struct {
		method, path, body string
	}{
		{http.MethodPost, "/register", `{"email":"a@b.ru","password":"p","role":"employee"}`},
		{http.MethodPost, "/login", `{"email":"a@b.ru","password":"p"}`},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			rec := serve(h, tt.method, tt.path, tt.body, "")

			assert.Equal(t, http.StatusNotImplemented, rec.Code)
			assertErrorBody(t, rec)
		})
	}
}

// Токен из /dummyLogin пускает в ручки своей роли и не пускает в чужие.
func TestDummyLogin(t *testing.T) {
	h := newTestRouter()

	t.Run("токен модератора", func(t *testing.T) {
		rec := serve(h, http.MethodPost, "/dummyLogin", `{"role":"moderator"}`, "")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

		var token string // по контракту Token — JSON-строка
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &token))
		require.NotEmpty(t, token)

		// Создавать ПВЗ модератору можно, принимать товары нельзя.
		assert.Equal(t, http.StatusCreated, serve(h, http.MethodPost, "/pvz", `{"city":"Москва"}`, token).Code)
		assert.Equal(t, http.StatusForbidden,
			serve(h, http.MethodPost, "/receptions", `{"pvzId":"6f1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"}`, token).Code)
	})

	tests := []struct {
		name, body string
	}{
		{"роль из текста задания", `{"role":"client"}`},
		{"роль не указана", `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(h, http.MethodPost, "/dummyLogin", tt.body, "")

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assertErrorBody(t, rec)
			assert.Contains(t, rec.Body.String(), "недопустимая роль")
		})
	}
}

func TestPostPVZ(t *testing.T) {
	h := newTestRouter()
	moderator := tokenFor(t, domain.RoleModerator)

	t.Run("ПВЗ заведён", func(t *testing.T) {
		// id и дата из тела игнорируются: их назначает сервис.
		rec := serve(h, http.MethodPost, "/pvz",
			`{"city":"Москва","id":"11111111-1111-1111-1111-111111111111","registrationDate":"2000-01-01T00:00:00Z"}`,
			moderator)

		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		assert.JSONEq(t,
			`{"id":"0192f1a4-7b3c-7d2e-8f00-000000000001","registrationDate":"2026-10-24T12:04:05.123456Z","city":"Москва"}`,
			rec.Body.String())
	})
	t.Run("город не из справочника", func(t *testing.T) {
		rec := serve(h, http.MethodPost, "/pvz", `{"city":"Томск"}`, moderator)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "город недоступен для открытия ПВЗ")
	})
	t.Run("внутренняя ошибка не уходит клиенту", func(t *testing.T) {
		rec := serve(h, http.MethodPost, "/pvz", `{"city":"boom"}`, moderator)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assertErrorBody(t, rec)
		assert.NotContains(t, rec.Body.String(), "10.0.0.5")
	})
}

func TestReceptions(t *testing.T) {
	h := newTestRouter()
	employee, moderator := tokenFor(t, domain.RoleEmployee), tokenFor(t, domain.RoleModerator)
	openBody := func(id uuid.UUID) string { return `{"pvzId":"` + id.String() + `"}` }
	closePath := func(id uuid.UUID) string { return "/pvz/" + id.String() + "/close_last_reception" }

	t.Run("приёмка открыта", func(t *testing.T) {
		rec := serve(h, http.MethodPost, "/receptions", openBody(pvzEmpty), employee)

		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{
			"id":"0192f1a4-7b3c-7d2e-8f00-0000000000a1",
			"dateTime":"2026-10-24T12:04:05.123456Z",
			"pvzId":"`+pvzEmpty.String()+`",
			"status":"in_progress"
		}`, rec.Body.String())
	})
	t.Run("приёмка закрыта", func(t *testing.T) {
		rec := serve(h, http.MethodPost, closePath(pvzWithReception), "", employee)

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{
			"id":"0192f1a4-7b3c-7d2e-8f00-0000000000a1",
			"dateTime":"2026-10-24T12:04:05.123456Z",
			"pvzId":"`+pvzWithReception.String()+`",
			"status":"close"
		}`, rec.Body.String())
	})

	// Нарушенные правила — 400 с текстом ошибки, как в контракте. Несуществующий
	// ПВЗ — 404: в контракте его нет, но так правильнее по HTTP.
	errorTests := []struct {
		name, method, path, body string
		wantStatus               int
		wantMsg                  string
	}{
		{"уже есть открытая приёмка", http.MethodPost, "/receptions", openBody(pvzWithReception),
			http.StatusBadRequest, "на ПВЗ уже есть незакрытая приёмка"},
		{"закрывать нечего", http.MethodPost, closePath(pvzEmpty), "",
			http.StatusBadRequest, "на ПВЗ нет открытой приёмки"},
		{"открытие на несуществующем ПВЗ", http.MethodPost, "/receptions", openBody(pvzMissing),
			http.StatusNotFound, "ПВЗ не найден"},
		{"закрытие на несуществующем ПВЗ", http.MethodPost, closePath(pvzMissing), "",
			http.StatusNotFound, "ПВЗ не найден"},
	}
	for _, tt := range errorTests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(h, tt.method, tt.path, tt.body, employee)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assertErrorBody(t, rec)
			assert.Contains(t, rec.Body.String(), tt.wantMsg)
		})
	}

	// Приёмками занимается только сотрудник ПВЗ.
	t.Run("модератору нельзя", func(t *testing.T) {
		assert.Equal(t, http.StatusForbidden,
			serve(h, http.MethodPost, "/receptions", openBody(pvzEmpty), moderator).Code)
		assert.Equal(t, http.StatusForbidden,
			serve(h, http.MethodPost, closePath(pvzWithReception), "", moderator).Code)
	})
}

func TestProducts(t *testing.T) {
	h := newTestRouter()
	employee, moderator := tokenFor(t, domain.RoleEmployee), tokenFor(t, domain.RoleModerator)
	addBody := func(id uuid.UUID, productType string) string {
		return `{"pvzId":"` + id.String() + `","type":"` + productType + `"}`
	}
	deletePath := func(id uuid.UUID) string { return "/pvz/" + id.String() + "/delete_last_product" }

	t.Run("товар добавлен", func(t *testing.T) {
		rec := serve(h, http.MethodPost, "/products", addBody(pvzWithReception, "обувь"), employee)

		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{
			"id":"0192f1a4-7b3c-7d2e-8f00-0000000000b1",
			"dateTime":"2026-10-24T12:04:05.123456Z",
			"receptionId":"0192f1a4-7b3c-7d2e-8f00-0000000000a1",
			"type":"обувь"
		}`, rec.Body.String())
	})
	t.Run("товар удалён", func(t *testing.T) {
		rec := serve(h, http.MethodPost, deletePath(pvzWithReception), "", employee)

		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Empty(t, rec.Body.String(), "в контракте у ответа 200 нет тела")
	})

	errorTests := []struct {
		name, method, path, body string
		wantStatus               int
		wantMsg                  string
	}{
		// Генератор не проверяет enum: недопустимый тип доходит до сервиса,
		// и тот отвечает понятной ошибкой со списком типов.
		{"тип не из справочника", http.MethodPost, "/products", addBody(pvzWithReception, "мебель"),
			http.StatusBadRequest, "недопустимый тип товара"},
		{"добавление без открытой приёмки", http.MethodPost, "/products", addBody(pvzEmpty, "обувь"),
			http.StatusBadRequest, "на ПВЗ нет открытой приёмки"},
		{"удалять нечего", http.MethodPost, deletePath(pvzEmpty), "",
			http.StatusBadRequest, "в приёмке нет товаров для удаления"},
		{"добавление на несуществующем ПВЗ", http.MethodPost, "/products", addBody(pvzMissing, "обувь"),
			http.StatusNotFound, "ПВЗ не найден"},
		{"удаление на несуществующем ПВЗ", http.MethodPost, deletePath(pvzMissing), "",
			http.StatusNotFound, "ПВЗ не найден"},
	}
	for _, tt := range errorTests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(h, tt.method, tt.path, tt.body, employee)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assertErrorBody(t, rec)
			assert.Contains(t, rec.Body.String(), tt.wantMsg)
		})
	}

	// Товарами занимается только сотрудник ПВЗ.
	t.Run("модератору нельзя", func(t *testing.T) {
		assert.Equal(t, http.StatusForbidden,
			serve(h, http.MethodPost, "/products", addBody(pvzWithReception, "обувь"), moderator).Code)
		assert.Equal(t, http.StatusForbidden,
			serve(h, http.MethodPost, deletePath(pvzWithReception), "", moderator).Code)
	})
}

func TestGetPVZ(t *testing.T) {
	employee, moderator := tokenFor(t, domain.RoleEmployee), tokenFor(t, domain.RoleModerator)
	newRouter := func(spy *listSpy) http.Handler {
		return NewRouter(slog.New(slog.DiscardHandler), Deps{DB: fakeDB{}, Tokens: testTokens, PVZ: spy})
	}

	t.Run("значения по умолчанию из контракта", func(t *testing.T) {
		spy := &listSpy{}
		rec := serve(newRouter(spy), http.MethodGet, "/pvz", "", employee)

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, 1, spy.page)
		assert.Equal(t, 10, spy.limit)
		assert.Equal(t, domain.Period{}, spy.period)
		// Пустой список товаров уходит как [], а не пропадает из ответа.
		assert.JSONEq(t, `[{
			"pvz":{"id":"0192f1a4-7b3c-7d2e-8f00-000000000001","registrationDate":"2026-10-24T12:04:05.123456Z","city":"Москва"},
			"receptions":[{
				"reception":{
					"id":"0192f1a4-7b3c-7d2e-8f00-0000000000a1",
					"dateTime":"2026-10-24T12:04:05.123456Z",
					"pvzId":"0192f1a4-7b3c-7d2e-8f00-000000000001",
					"status":"in_progress"
				},
				"products":[]
			}]
		}]`, rec.Body.String())
	})
	t.Run("параметры доходят до сервиса", func(t *testing.T) {
		spy := &listSpy{}
		rec := serve(newRouter(spy), http.MethodGet,
			"/pvz?page=3&limit=30&startDate=2026-10-01T00:00:00Z&endDate=2026-10-31T23:59:59%2B03:00", "", moderator)

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, 3, spy.page)
		assert.Equal(t, 30, spy.limit)
		require.NotNil(t, spy.period.Start)
		require.NotNil(t, spy.period.End)
		assert.True(t, spy.period.Start.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)))
		assert.True(t, spy.period.End.Equal(time.Date(2026, 10, 31, 20, 59, 59, 0, time.UTC)))
	})
	t.Run("пустой список — это []", func(t *testing.T) {
		h := NewRouter(slog.New(slog.DiscardHandler), Deps{DB: fakeDB{}, Tokens: testTokens, PVZ: emptyList{}})
		rec := serve(h, http.MethodGet, "/pvz", "", employee)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `[]`, rec.Body.String())
	})
	t.Run("неверные параметры", func(t *testing.T) {
		spy := &listSpy{err: fmt.Errorf("%w: limit должен быть от 1 до 30", domain.ErrInvalidListParams)}
		rec := serve(newRouter(spy), http.MethodGet, "/pvz?limit=31", "", employee)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assertErrorBody(t, rec)
		assert.Contains(t, rec.Body.String(), "limit должен быть от 1 до 30")
	})
}

// emptyList — PVZService, у которого список ПВЗ пуст.
type emptyList struct{ fakePVZ }

func (emptyList) List(context.Context, domain.Period, int, int) ([]domain.PVZWithReceptions, error) {
	return []domain.PVZWithReceptions{}, nil
}

// Запрос, который не разбирается по контракту, получает 400 до вызова ручки.
func TestBadRequest(t *testing.T) {
	h := newTestRouter()
	employee, moderator := tokenFor(t, domain.RoleEmployee), tokenFor(t, domain.RoleModerator)

	tests := []struct {
		name, method, path, body, token string
	}{
		{"невалидный JSON", http.MethodPost, "/pvz", `{"city":`, moderator},
		{"UUID в пути", http.MethodPost, "/pvz/not-a-uuid/close_last_reception", "", employee},
		{"UUID в теле", http.MethodPost, "/receptions", `{"pvzId":"42"}`, employee},
		{"число в query", http.MethodGet, "/pvz?page=first", "", employee},
		{"дата в query", http.MethodGet, "/pvz?startDate=вчера", "", moderator},
		{"невалидный JSON в открытой ручке", http.MethodPost, "/dummyLogin", `{"role":`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(h, tt.method, tt.path, tt.body, tt.token)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assertErrorBody(t, rec)
		})
	}
}

func TestDocs(t *testing.T) {
	h := newTestRouter()

	t.Run("корень ведёт в Swagger UI", func(t *testing.T) {
		rec := serve(h, http.MethodGet, "/", "", "")
		assert.Equal(t, http.StatusFound, rec.Code)
		assert.Equal(t, "/docs/", rec.Header().Get("Location"))
	})
	t.Run("страница Swagger UI", func(t *testing.T) {
		rec := serve(h, http.MethodGet, "/docs/", "", "")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
		assert.Contains(t, rec.Body.String(), `url: "openapi.yaml"`)
	})
	t.Run("/docs без слеша", func(t *testing.T) {
		rec := serve(h, http.MethodGet, "/docs", "", "")
		// Редирект делает сам ServeMux; код зависит от версии Go (301 или 307).
		assert.Contains(t, []int{http.StatusMovedPermanently, http.StatusTemporaryRedirect}, rec.Code)
		assert.Equal(t, "/docs/", rec.Header().Get("Location"))
	})
	t.Run("спека отдаётся без изменений", func(t *testing.T) {
		rec := serve(h, http.MethodGet, "/docs/openapi.yaml", "", "")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, api.OpenAPI, rec.Body.Bytes())
	})
	t.Run("лишние пути под /docs/ не отдаются", func(t *testing.T) {
		rec := serve(h, http.MethodGet, "/docs/whatever", "", "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

// serve выполняет запрос; token, если не пустой, уходит в Authorization.
func serve(h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// assertErrorBody проверяет формат ошибки из контракта: {"message": "..."}.
func assertErrorBody(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var body struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	assert.NotEmpty(t, body.Message)
}

func TestLogRequests(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantStatus int
		wantBytes  int
	}{
		{
			name:       "статус по умолчанию 200",
			handler:    func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hello")) },
			wantStatus: http.StatusOK,
			wantBytes:  5,
		},
		{
			name: "явный статус",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTeapot)
				_, _ = w.Write([]byte("{}"))
			},
			wantStatus: http.StatusTeapot,
			wantBytes:  2,
		},
		{
			name: "повторный WriteHeader не меняет статус",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusCreated)
				w.WriteHeader(http.StatusInternalServerError)
			},
			wantStatus: http.StatusCreated,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewJSONHandler(&buf, nil))

			rec := httptest.NewRecorder()
			logRequests(log, nil, tt.handler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/pvz?page=2", nil))

			var entry struct {
				Msg    string `json:"msg"`
				Method string `json:"method"`
				Path   string `json:"path"`
				Status int    `json:"status"`
				Bytes  int    `json:"bytes"`
				IP     string `json:"client_ip"`
				// Указатель: отличает «поля нет» от нулевой длительности.
				DurationMS *float64 `json:"duration_ms"`
			}
			require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
			assert.Equal(t, "http request", entry.Msg)
			assert.Equal(t, http.MethodGet, entry.Method)
			assert.Equal(t, "/pvz", entry.Path, "query не попадает в лог")
			assert.Equal(t, tt.wantStatus, entry.Status)
			assert.Equal(t, tt.wantBytes, entry.Bytes)
			assert.Equal(t, "192.0.2.1", entry.IP, "адрес, который ставит httptest")
			assert.NotNil(t, entry.DurationMS)
			assert.Equal(t, tt.wantStatus, rec.Code, "статус доходит до клиента")
		})
	}
}

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("172.30.0.2/32"), netip.MustParsePrefix("10.0.0.0/8")}

	tests := []struct {
		name       string
		remoteAddr string
		xff        []string // каждый элемент — отдельный заголовок X-Forwarded-For
		want       string
	}{
		{name: "без прокси", remoteAddr: "203.0.113.7:5555", want: "203.0.113.7"},
		{
			// Клиент напрямую, без прокси: его заголовку не верим.
			name:       "заголовок от недоверенного адреса",
			remoteAddr: "203.0.113.7:5555", xff: []string{"1.2.3.4"},
			want: "203.0.113.7",
		},
		{name: "через Caddy", remoteAddr: "172.30.0.2:40000", xff: []string{"198.51.100.9"}, want: "198.51.100.9"},
		{
			// Клиент сам прислал X-Forwarded-For, Caddy дописал настоящий адрес
			// справа: подделка левее не используется.
			name:       "подделка слева",
			remoteAddr: "172.30.0.2:40000", xff: []string{"6.6.6.6, 198.51.100.9"},
			want: "198.51.100.9",
		},
		{
			name:       "цепочка доверенных прокси",
			remoteAddr: "172.30.0.2:40000", xff: []string{"198.51.100.9, 10.0.0.5"},
			want: "198.51.100.9",
		},
		{
			name:       "несколько заголовков",
			remoteAddr: "172.30.0.2:40000", xff: []string{"6.6.6.6", "198.51.100.9"},
			want: "198.51.100.9",
		},
		{name: "IPv6", remoteAddr: "172.30.0.2:40000", xff: []string{"2001:db8::1"}, want: "2001:db8::1"},
		{
			name:       "испорченный заголовок",
			remoteAddr: "172.30.0.2:40000", xff: []string{"мусор, 198.51.100.9"},
			want: "198.51.100.9",
		},
		{name: "от прокси без заголовка", remoteAddr: "172.30.0.2:40000", want: "172.30.0.2"},
		{name: "адрес не разбирается", remoteAddr: "pipe", want: "pipe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remoteAddr
			for _, v := range tt.xff {
				r.Header.Add("X-Forwarded-For", v)
			}

			assert.Equal(t, tt.want, clientIP(r, trusted))
		})
	}
}

// Тело больше 1 МБ не дочитывается: 413 с ошибкой в формате контракта.
func TestBodyLimit(t *testing.T) {
	h := newTestRouter()
	moderator := tokenFor(t, domain.RoleModerator)

	big := `{"city":"Москва","pad":"` + strings.Repeat("x", maxBodyBytes) + `"}`
	rec := serve(h, http.MethodPost, "/pvz", big, moderator)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assertErrorBody(t, rec)
	assert.Contains(t, rec.Body.String(), "тело запроса больше 1 МБ")
}
