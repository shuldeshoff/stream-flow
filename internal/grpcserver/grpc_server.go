package grpcserver

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sul/streamflow/internal/metrics"
	"github.com/sul/streamflow/internal/models"
	"github.com/sul/streamflow/internal/processor"
	pb "github.com/sul/streamflow/pkg/grpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type GRPCServer struct {
	pb.UnimplementedEventIngestionServer
	processor *processor.EventProcessor
	server    *grpc.Server
	port      int
}

func NewGRPCServer(port int, proc *processor.EventProcessor) *GRPCServer {
	return &GRPCServer{
		processor: proc,
		port:      port,
	}
}

func (s *GRPCServer) Start() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	s.server = grpc.NewServer(
		grpc.MaxRecvMsgSize(10 * 1024 * 1024), // 10MB
		grpc.MaxSendMsgSize(10 * 1024 * 1024),
	)

	pb.RegisterEventIngestionServer(s.server, s)

	log.Info().Int("port", s.port).Msg("gRPC server listening")

	return s.server.Serve(lis)
}

func (s *GRPCServer) Stop() {
	if s.server != nil {
		s.server.GracefulStop()
		log.Info().Msg("gRPC server stopped")
	}
}

func (s *GRPCServer) SendEvent(ctx context.Context, req *pb.Event) (*pb.EventResponse, error) {
	startTime := time.Now()
	defer func() {
		metrics.RecordIngestionLatency(time.Since(startTime).Seconds())
	}()

	// Конвертируем proto в внутреннюю модель
	event := protoToEvent(req)

	// Отправляем на обработку
	if err := s.processor.Submit(event); err != nil {
		metrics.IncEventsReceived("dropped")
		return &pb.EventResponse{
			Status:  "error",
			Id:      req.Id,
			Message: err.Error(),
		}, nil
	}

	metrics.IncEventsReceived("accepted")

	return &pb.EventResponse{
		Status: "accepted",
		Id:     req.Id,
	}, nil
}

func (s *GRPCServer) SendBatch(ctx context.Context, req *pb.EventBatch) (*pb.BatchResponse, error) {
	startTime := time.Now()
	defer func() {
		metrics.RecordIngestionLatency(time.Since(startTime).Seconds())
	}()

	accepted := int32(0)
	failed := int32(0)

	for _, protoEvent := range req.Events {
		event := protoToEvent(protoEvent)

		if err := s.processor.Submit(event); err != nil {
			failed++
		} else {
			accepted++
		}
	}

	metrics.IncBatchEventsReceived(int(accepted), int(failed))

	return &pb.BatchResponse{
		Status:   "completed",
		Accepted: accepted,
		Failed:   failed,
		Total:    int32(len(req.Events)),
	}, nil
}

func (s *GRPCServer) SendStream(stream pb.EventIngestion_SendStreamServer) error {
	log.Debug().Msg("Stream connection established")

	eventCount := 0
	startTime := time.Now()

	for {
		protoEvent, err := stream.Recv()
		if err == io.EOF {
			log.Debug().
				Int("events", eventCount).
				Dur("duration", time.Since(startTime)).
				Msg("Stream closed")
			return nil
		}
		if err != nil {
			log.Error().Err(err).Msg("Stream receive error")
			return err
		}

		event := protoToEvent(protoEvent)

		if err := s.processor.Submit(event); err != nil {
			metrics.IncEventsReceived("dropped")
			if err := stream.Send(&pb.EventResponse{
				Status:  "error",
				Id:      protoEvent.Id,
				Message: err.Error(),
			}); err != nil {
				return err
			}
			continue
		}

		metrics.IncEventsReceived("accepted")
		eventCount++

		// Отправляем подтверждение
		if err := stream.Send(&pb.EventResponse{
			Status: "accepted",
			Id:     protoEvent.Id,
		}); err != nil {
			return err
		}
	}
}

func protoToEvent(pe *pb.Event) models.Event {
	// Конвертируем map[string]string в map[string]interface{}
	data := make(map[string]interface{})
	for k, v := range pe.Data {
		data[k] = v
	}

	metadata := pe.Metadata
	if metadata == nil {
		metadata = make(map[string]string)
	}

	timestamp := time.Now()
	if pe.Timestamp != nil {
		timestamp = pe.Timestamp.AsTime()
	}

	return models.Event{
		ID:        pe.Id,
		Type:      pe.Type,
		Source:    pe.Source,
		Timestamp: timestamp,
		Data:      data,
		Metadata:  metadata,
	}
}

func eventToProto(e models.Event) *pb.Event {
	// Конвертируем map[string]interface{} в map[string]string
	data := make(map[string]string)
	for k, v := range e.Data {
		data[k] = fmt.Sprintf("%v", v)
	}

	return &pb.Event{
		Id:        e.ID,
		Type:      e.Type,
		Source:    e.Source,
		Timestamp: timestamppb.New(e.Timestamp),
		Data:      data,
		Metadata:  e.Metadata,
	}
}

