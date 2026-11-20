package grpcapi

import (
	"context"
	"runtime/debug"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UnaryRecover превращает панику обработчика метода в отказ INTERNAL: соединение и остальные
// вызовы того же сервера продолжают работать.
//
// Значение паники в журнал не попадает — его формирует код обработчика, и в него могут войти
// оригинальный URL или учётные данные. Стек вызовов аргументы не показывает, поэтому отказа
// достаточно для разбора причины.
func UnaryRecover() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if recover() == nil {
				return
			}

			logrus.WithField("method", info.FullMethod).
				WithField("stack", string(debug.Stack())).
				Error("gRPC: обработка прервана паникой")

			resp, err = nil, status.Error(codes.Internal, msgInternal)
		}()

		return handler(ctx, req)
	}
}
