// Commande parent-stub : faux minihub parent pour la Phase 0 (walking skeleton).
// Tient lieu du serveur gRPC enfant du minihub Java tant que l'intégration réelle
// (Phase 1+) n'est pas faite. Accepte la connexion sortante du miniMiniHub,
// répond aux heartbeats et pousse un Ping périodique dans le PollCommand.
//
// PAS destiné à la production — outil de preuve de tunnel uniquement.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/grpc"

	pb "github.com/jmlmvi/miniminihub/proto/mmhpb"
)

type server struct {
	pb.UnimplementedMiniMiniHubControlServer
	log     *slog.Logger
	pingSec int
	hbCount atomic.Uint64
}

func (s *server) Heartbeat(_ context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	n := s.hbCount.Add(1)
	s.log.Info("heartbeat received", "slug", req.Slug, "id", req.MiniminihubId,
		"seq", req.Sequence, "total", n)
	return &pb.HeartbeatResponse{
		Accepted:       true,
		ServerTsMs:     time.Now().UnixMilli(),
		NextIntervalMs: 30000,
		TraceId:        "stub-trace",
	}, nil
}

func (s *server) PollCommand(req *pb.PollRequest, stream pb.MiniMiniHubControl_PollCommandServer) error {
	s.log.Info("child connected to PollCommand", "slug", req.Slug, "id", req.MiniminihubId)
	ctx := stream.Context()
	ticker := time.NewTicker(time.Duration(s.pingSec) * time.Second)
	defer ticker.Stop()

	var i uint64
	for {
		select {
		case <-ctx.Done():
			s.log.Info("child stream closed", "slug", req.Slug)
			return ctx.Err()
		case <-ticker.C:
			i++
			cmd := &pb.Command{
				CommandId:  "ping-" + time.Now().Format("150405"),
				IssuedAtMs: time.Now().UnixMilli(),
				Payload:    &pb.Command_Ping{Ping: &pb.PingCommand{Note: "hello from parent-stub"}},
			}
			if err := stream.Send(cmd); err != nil {
				s.log.Error("push ping failed", "err", err)
				return err
			}
			s.log.Info("pushed ping", "n", i, "to", req.Slug)
		}
	}
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	addr := os.Getenv("STUB_ADDR")
	if addr == "" {
		addr = ":7443"
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Error("listen", "err", err)
		os.Exit(1)
	}

	grpcSrv := grpc.NewServer()
	pb.RegisterMiniMiniHubControlServer(grpcSrv, &server{log: log, pingSec: 15})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Info("shutting down")
		grpcSrv.GracefulStop()
	}()

	log.Info("parent-stub listening", "addr", addr, "ping_every_s", 15)
	if err := grpcSrv.Serve(lis); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}
