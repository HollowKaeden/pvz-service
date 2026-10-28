// Package grpctransport — gRPC-слой pvz (не путать с пакетом grpc): метод
// GetPVZList из api/pvz.proto. Здесь только приём и отдача вызовов,
// бизнес-правила живут в service.
package grpctransport

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/HollowKaeden/pvz-service/internal/domain"
	"github.com/HollowKaeden/pvz-service/internal/transport/grpc/pvzv1"
)

// PVZService — список ПВЗ.
type PVZService interface {
	All(ctx context.Context) ([]domain.PVZ, error)
}

// CallMetrics учитывает обработанные вызовы.
type CallMetrics interface {
	ObserveGRPC(method, code string, d time.Duration)
}

// NewServer собирает gRPC-сервер: метод GetPVZList, reflection, лог и
// метрики каждого вызова (metrics nil — без метрик). Reflection отдаёт
// описание методов, поэтому grpcurl работает без proto-файла.
func NewServer(log *slog.Logger, pvz PVZService, metrics CallMetrics) *grpc.Server {
	// Первый перехватчик внешний: метрики видят код ответа уже после того,
	// как logCalls превратил неожиданную ошибку в Internal.
	var interceptors []grpc.UnaryServerInterceptor
	if metrics != nil {
		interceptors = append(interceptors, measureCalls(metrics))
	}
	interceptors = append(interceptors, logCalls(log))
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(interceptors...))
	pvzv1.RegisterPVZServiceServer(srv, api{pvz: pvz})
	reflection.Register(srv)
	return srv
}

// api реализует pvzv1.PVZServiceServer.
type api struct {
	pvzv1.UnimplementedPVZServiceServer
	pvz PVZService
}

// GetPVZList возвращает все ПВЗ, новые сверху. Авторизации нет: так требует
// задание.
func (a api) GetPVZList(ctx context.Context, _ *pvzv1.GetPVZListRequest) (*pvzv1.GetPVZListResponse, error) {
	list, err := a.pvz.All(ctx)
	if err != nil {
		return nil, err
	}
	resp := &pvzv1.GetPVZListResponse{Pvzs: make([]*pvzv1.PVZ, len(list))}
	for i, p := range list {
		resp.Pvzs[i] = &pvzv1.PVZ{
			Id:               p.ID.String(),
			RegistrationDate: timestamppb.New(p.RegistrationDate),
			City:             p.City,
		}
	}
	return resp, nil
}

// logCalls пишет по строке лога на каждый вызов: метод, код ответа и время.
// Ошибка без gRPC-кода — неожиданная: подробности уходят только в лог, а
// клиент получает Internal с общим текстом, как 500 в HTTP.
func logCalls(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)

		_, hasCode := status.FromError(err) // nil тоже считается: код OK
		switch {
		case hasCode:
		// Клиент ушёл или истёк его дедлайн: это не ошибка сервиса.
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			err = status.FromContextError(err).Err()
		default:
			log.ErrorContext(ctx, "ошибка обработки вызова",
				slog.String("method", info.FullMethod),
				slog.Any("error", err),
			)
			err = status.Error(codes.Internal, "внутренняя ошибка сервера")
		}

		log.LogAttrs(ctx, slog.LevelInfo, "grpc call",
			slog.String("method", info.FullMethod),
			slog.String("code", status.Code(err).String()),
			slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
		)
		return resp, err
	}
}

// measureCalls учитывает каждый вызов: метод, код ответа и время обработки.
// Метод берётся из описания сервера, поэтому набор его значений ограничен:
// вызов несуществующего метода до перехватчиков не доходит.
func measureCalls(m CallMetrics) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		m.ObserveGRPC(info.FullMethod, status.Code(err).String(), time.Since(start))
		return resp, err
	}
}
