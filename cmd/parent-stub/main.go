// Commande parent-stub : faux minihub parent pour la Phase 0 (walking skeleton).
// Tient lieu du serveur gRPC enfant du minihub Java tant que l'intégration réelle
// (Phase 1+) n'est pas faite. Accepte la connexion sortante du miniMiniHub,
// répond aux heartbeats et pousse un Ping périodique dans le PollCommand.
//
// PAS destiné à la production — outil de preuve de tunnel uniquement.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

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

// mtlsCreds construit des credentials serveur exigeant un cert client valide.
func mtlsCreds(certPath, keyPath, caPath string) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS12,
	}), nil
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

	var opts []grpc.ServerOption
	if certPath := os.Getenv("TLS_CERT"); certPath != "" {
		creds, err := mtlsCreds(certPath, os.Getenv("TLS_KEY"), os.Getenv("TLS_CA"))
		if err != nil {
			log.Error("mtls setup", "err", err)
			os.Exit(1)
		}
		opts = append(opts, grpc.Creds(creds))
		log.Info("mTLS enabled (RequireAndVerifyClientCert)")
	} else {
		log.Info("plaintext mode (no TLS_CERT set)")
	}

	grpcSrv := grpc.NewServer(opts...)
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
