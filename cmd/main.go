package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
	"vk2/internal/config"
	pb "vk2/internal/proto"
	grpcserver "vk2/internal/server"
	"vk2/internal/subpub"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {

	cfg, err := config.Load()
	if err != nil {

		fmt.Fprintf(os.Stderr, "Не удалось загрузить конфигурацию: %v\n", err)
		os.Exit(1)
	}

	logger, err := setupLogger(cfg.LogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Не удалось настроить логгер: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("Запуск SubPub gRPC сервера", zap.Int("port", cfg.GRPCPort), zap.String("log_level", cfg.LogLevel))

	spSystem := subpub.NewSubPub()

	pubSubService := grpcserver.NewGrpcServer(spSystem, logger)

	grpcServer := grpc.NewServer()
	pb.RegisterPubSubServer(grpcServer, pubSubService)
	reflection.Register(grpcServer) // Включаем серверную рефлексию

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		logger.Fatal("Не удалось начать прослушивание", zap.Error(err), zap.Int("port", cfg.GRPCPort))
	}

	go func() {
		logger.Info("gRPC сервер слушает", zap.String("address", lis.Addr().String()))
		if err := grpcServer.Serve(lis); err != nil && err != grpc.ErrServerStopped {
			logger.Error("Ошибка при обслуживании gRPC запросов", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Info("Получен сигнал завершения работы", zap.String("signal", sig.String()))

	_, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	logger.Info("Попытка корректной остановки gRPC сервера...")
	grpcServer.GracefulStop() // Блокируется до завершения всех активных RPC или истечения таймаута контекста
	logger.Info("gRPC сервер остановлен")

	closeSubPubCtx, closeSubPubCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer closeSubPubCancel()

	logger.Info("Закрытие системы SubPub...")
	if err := spSystem.Close(closeSubPubCtx); err != nil {
		logger.Error("Ошибка во время закрытия системы SubPub", zap.Error(err))
	} else {
		logger.Info("Система SubPub успешно закрыта")
	}

	logger.Info("Сервер корректно завершил работу")
}

func setupLogger(levelStr string) (*zap.Logger, error) {
	var logLevel zapcore.Level
	if err := logLevel.Set(levelStr); err != nil {
		return nil, fmt.Errorf("неверный уровень логирования '%s': %w", levelStr, err)
	}

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder // Формат времени

	cfg := zap.Config{
		Level:            zap.NewAtomicLevelAt(logLevel),
		Development:      false,
		Encoding:         "json",
		EncoderConfig:    encoderCfg,
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
	}

	if os.Getenv("APP_ENV") == "development" {
		cfg.Development = true
		cfg.Encoding = "console"
		cfg.EncoderConfig = zap.NewDevelopmentEncoderConfig()
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	return cfg.Build()
}
