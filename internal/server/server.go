package grpcserver

import (
	"context"
	"fmt"
	pb "vk2/internal/proto"
	"vk2/internal/subpub"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type GrpcServer struct {
	pb.UnimplementedPubSubServer
	sp     subpub.SubPub
	logger *zap.Logger
}

func NewGrpcServer(sp subpub.SubPub, logger *zap.Logger) *GrpcServer {
	return &GrpcServer{
		sp:     sp,
		logger: logger,
	}
}

func (s *GrpcServer) Publish(ctx context.Context, req *pb.PublishRequest) (*emptypb.Empty, error) {
	if req.GetKey() == "" {
		s.logger.Warn("Publish вызван с пустым ключом")
		return nil, status.Error(codes.InvalidArgument, "ключ не может быть пустым")
	}
	if req.GetData() == "" {
		s.logger.Debug("Publish вызван с пустыми данными", zap.String("key", req.GetKey()))
	}

	s.logger.Info("Публикация сообщения", zap.String("key", req.GetKey()), zap.Int("data_len", len(req.GetData())))

	err := s.sp.Publish(req.GetKey(), req.GetData())
	if err != nil {
		s.logger.Error("Не удалось опубликовать сообщение в систему subpub", zap.Error(err), zap.String("key", req.GetKey()))

		if err.Error() == "subpub: система закрыта" {
			return nil, status.Error(codes.Unavailable, "система находится в процессе завершения работы")
		}
		return nil, status.Errorf(codes.Internal, "не удалось опубликовать сообщение: %v", err)
	}

	return &emptypb.Empty{}, nil
}

func (s *GrpcServer) Subscribe(req *pb.SubscribeRequest, stream pb.PubSub_SubscribeServer) error {
	if req.GetKey() == "" {
		s.logger.Warn("Subscribe вызван с пустым ключом")
		return status.Error(codes.InvalidArgument, "ключ не может быть пустым")
	}

	s.logger.Info("Клиент подписывается", zap.String("key", req.GetKey()))

	var sub subpub.Subscription

	handler := func(msg interface{}) {
		data, ok := msg.(string)
		if !ok {
			s.logger.Error("Получено не строковое сообщение из subpub для gRPC потока",
				zap.String("key", req.GetKey()),
				zap.Any("type", fmt.Sprintf("%T", msg)))
			return
		}

		event := &pb.Event{
			Data: data,
		}

		if err := stream.Send(event); err != nil {
			s.logger.Error("Не удалось отправить событие клиенту", zap.Error(err), zap.String("key", req.GetKey()))

			if sub != nil {
				s.logger.Info("Отписка из-за ошибки отправки в поток", zap.String("key", req.GetKey()))
				sub.Unsubscribe()
			}
		}
	}

	var err error
	sub, err = s.sp.Subscribe(req.GetKey(), handler)
	if err != nil {
		s.logger.Error("Не удалось подписаться на систему subpub", zap.Error(err), zap.String("key", req.GetKey()))
		if err.Error() == "subpub: система закрыта" {
			return status.Error(codes.Unavailable, "система находится в процессе завершения работы")
		}
		return status.Errorf(codes.Internal, "не удалось подписаться: %v", err)
	}
	s.logger.Info("Клиент успешно подписан", zap.String("key", req.GetKey()))
	ctx := stream.Context()
	<-ctx.Done()

	s.logger.Info("Контекст подписки клиента завершен, отписываемся", zap.String("key", req.GetKey()), zap.Error(ctx.Err()))
	sub.Unsubscribe()

	if ctx.Err() == context.Canceled {
		s.logger.Info("Клиент отменил подписку", zap.String("key", req.GetKey()))
		return status.Error(codes.Canceled, "клиент отменил подписку")
	}
	if ctx.Err() == context.DeadlineExceeded {
		s.logger.Warn("Превышен дедлайн контекста подписки", zap.String("key", req.GetKey()))
		return status.Error(codes.DeadlineExceeded, "превышен дедлайн подписки")
	}

	return nil
}
