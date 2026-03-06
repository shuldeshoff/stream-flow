package main

import (
	"context"
	"fmt"
	"log"
	"time"

	pb "github.com/shuldeshoff/stream-flow/pkg/grpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func main() {
	// Подключаемся к gRPC серверу
	conn, err := grpc.NewClient("localhost:9000", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := pb.NewEventIngestionClient(conn)
	ctx := context.Background()

	fmt.Println("🔗 Connected to StreamFlow gRPC server")

	// Тест 1: Одиночное событие
	fmt.Println("\n📤 Test 1: Sending single event via gRPC...")
	resp, err := client.SendEvent(ctx, &pb.Event{
		Id:        "grpc-test-1",
		Type:      "test_event",
		Source:    "grpc_client",
		Timestamp: timestamppb.Now(),
		Data: map[string]string{
			"message": "Hello from gRPC",
			"value":   "42",
		},
	})
	if err != nil {
		log.Fatalf("SendEvent failed: %v", err)
	}
	fmt.Printf("✅ Response: %s (ID: %s)\n", resp.Status, resp.Id)

	// Тест 2: Батч событий
	fmt.Println("\n📦 Test 2: Sending batch via gRPC...")
	events := make([]*pb.Event, 100)
	for i := 0; i < 100; i++ {
		events[i] = &pb.Event{
			Id:        fmt.Sprintf("grpc-batch-%d", i),
			Type:      "batch_event",
			Source:    "grpc_client",
			Timestamp: timestamppb.Now(),
			Data: map[string]string{
				"index": fmt.Sprintf("%d", i),
			},
		}
	}

	batchResp, err := client.SendBatch(ctx, &pb.EventBatch{Events: events})
	if err != nil {
		log.Fatalf("SendBatch failed: %v", err)
	}
	fmt.Printf("✅ Batch: %s (Accepted: %d, Failed: %d, Total: %d)\n",
		batchResp.Status, batchResp.Accepted, batchResp.Failed, batchResp.Total)

	// Тест 3: Stream
	fmt.Println("\n🌊 Test 3: Streaming events via gRPC...")
	stream, err := client.SendStream(ctx)
	if err != nil {
		log.Fatalf("SendStream failed: %v", err)
	}

	// Отправляем события в stream
	go func() {
		for i := 0; i < 50; i++ {
			err := stream.Send(&pb.Event{
				Id:        fmt.Sprintf("grpc-stream-%d", i),
				Type:      "stream_event",
				Source:    "grpc_client",
				Timestamp: timestamppb.Now(),
				Data: map[string]string{
					"counter": fmt.Sprintf("%d", i),
				},
			})
			if err != nil {
				log.Printf("Stream send error: %v", err)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		stream.CloseSend()
	}()

	// Получаем подтверждения
	count := 0
	for {
		resp, err := stream.Recv()
		if err != nil {
			break
		}
		count++
		if count%10 == 0 {
			fmt.Printf("📨 Received %d acknowledgments...\n", count)
		}
	}
	fmt.Printf("✅ Stream completed: %d events acknowledged\n", count)

	// Тест 4: Load test
	fmt.Println("\n🔥 Test 4: Load test (10K events via gRPC)...")
	startTime := time.Now()

	for i := 0; i < 10000; i++ {
		_, err := client.SendEvent(ctx, &pb.Event{
			Id:        fmt.Sprintf("grpc-load-%d", i),
			Type:      "load_test",
			Source:    "grpc_client",
			Timestamp: timestamppb.Now(),
			Data: map[string]string{
				"index": fmt.Sprintf("%d", i),
			},
		})
		if err != nil {
			log.Printf("Error sending event %d: %v", i, err)
		}

		if (i+1)%1000 == 0 {
			fmt.Printf("  Sent %d events...\n", i+1)
		}
	}

	duration := time.Since(startTime)
	rps := float64(10000) / duration.Seconds()
	fmt.Printf("✅ Load test completed in %v (%.0f events/sec)\n", duration, rps)

	fmt.Println("\n✨ All gRPC tests completed!")
}

