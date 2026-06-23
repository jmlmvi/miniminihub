// Commande parent-stub : faux minihub parent pour les Phases 0-3 (preuve de tunnel).
// Tient lieu du serveur gRPC enfant du minihub Java tant que l'intégration réelle
// n'est pas faite. Côté Phase 3, il expose un proxy SOCKS5 + HTTP-CONNECT : les
// connexions clientes sont relayées à l'enfant (exit node) via EgressOpenCommand +
// EgressStream — le trafic sort sur Internet depuis l'IP de la VM enfant.
//
// PAS destiné à la production — outil de preuve uniquement.
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	pb "github.com/jmlmvi/miniminihub/proto/mmhpb"
)

// childConn = flux PollCommand vers l'enfant (Send sérialisé).
type childConn struct {
	mu     sync.Mutex
	stream pb.MiniMiniHubControl_PollCommandServer
}

func (c *childConn) send(cmd *pb.Command) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stream.Send(cmd)
}

type server struct {
	pb.UnimplementedMiniMiniHubControlServer
	log     *slog.Logger
	pingSec int
	hbCount atomic.Uint64
	connSeq atomic.Uint64

	mu      sync.Mutex
	child   *childConn
	pending map[string]net.Conn // conn_id -> connexion cliente en attente d'EgressStream
}

func (s *server) Heartbeat(_ context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	n := s.hbCount.Add(1)
	s.log.Info("heartbeat received", "slug", req.Slug, "seq", req.Sequence, "total", n)
	return &pb.HeartbeatResponse{
		Accepted: true, ServerTsMs: time.Now().UnixMilli(),
		NextIntervalMs: 30000, TraceId: "stub-trace",
	}, nil
}

func (s *server) PollCommand(req *pb.PollRequest, stream pb.MiniMiniHubControl_PollCommandServer) error {
	s.log.Info("child connected to PollCommand", "slug", req.Slug)
	cc := &childConn{stream: stream}
	s.mu.Lock()
	s.child = cc
	s.mu.Unlock()

	ctx := stream.Context()
	ticker := time.NewTicker(time.Duration(s.pingSec) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("child stream closed", "slug", req.Slug)
			s.mu.Lock()
			if s.child == cc {
				s.child = nil
			}
			s.mu.Unlock()
			return ctx.Err()
		case <-ticker.C:
			_ = cc.send(&pb.Command{
				CommandId:  "ping",
				IssuedAtMs: time.Now().UnixMilli(),
				Payload:    &pb.Command_Ping{Ping: &pb.PingCommand{Note: "keepalive"}},
			})
		}
	}
}

// EgressStream relaie le trafic entre la connexion cliente (en attente sous conn_id)
// et l'enfant qui a ouvert la sortie Internet.
func (s *server) EgressStream(stream pb.MiniMiniHubControl_EgressStreamServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	cid := first.ConnId
	client := s.takePending(cid)
	if client == nil {
		s.log.Warn("egress stream: no pending client", "conn_id", cid)
		return fmt.Errorf("no pending client for %s", cid)
	}
	defer client.Close()

	if first.Close {
		s.log.Warn("egress upstream failed", "conn_id", cid, "error", first.Error)
		return nil
	}
	s.log.Info("egress relaying", "conn_id", cid)

	// client -> stream
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := client.Read(buf)
			if n > 0 {
				if serr := stream.Send(&pb.EgressFrame{Data: buf[:n]}); serr != nil {
					return
				}
			}
			if rerr != nil {
				_ = stream.Send(&pb.EgressFrame{Close: true})
				return
			}
		}
	}()

	// stream -> client
	for {
		f, rerr := stream.Recv()
		if rerr != nil {
			return nil
		}
		if len(f.Data) > 0 {
			if _, werr := client.Write(f.Data); werr != nil {
				return nil
			}
		}
		if f.Close {
			return nil
		}
	}
}

func (s *server) registerPending(id string, c net.Conn) {
	s.mu.Lock()
	s.pending[id] = c
	s.mu.Unlock()
}

func (s *server) takePending(id string) net.Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.pending[id]
	delete(s.pending, id)
	return c
}

// openEgress alloue un conn_id, enregistre le client et pousse l'ordre à l'enfant.
func (s *server) openEgress(client net.Conn, host string, port uint16) bool {
	s.mu.Lock()
	child := s.child
	s.mu.Unlock()
	if child == nil {
		s.log.Warn("no child connected, cannot egress", "host", host)
		return false
	}
	cid := "c" + strconv.FormatUint(s.connSeq.Add(1), 10)
	s.registerPending(cid, client)
	err := child.send(&pb.Command{
		CommandId:  "egress-" + cid,
		IssuedAtMs: time.Now().UnixMilli(),
		Payload: &pb.Command_EgressOpen{EgressOpen: &pb.EgressOpenCommand{
			ConnId: cid, Host: host, Port: uint32(port),
		}},
	})
	if err != nil {
		s.takePending(cid)
		s.log.Error("push egress_open failed", "err", err)
		return false
	}
	s.log.Info("egress requested to child", "conn_id", cid, "host", host, "port", port)
	return true
}

// --- SOCKS5 (CONNECT, no-auth) -------------------------------------------------

func (s *server) serveSocks(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go s.handleSocks(c)
	}
}

func (s *server) handleSocks(c net.Conn) {
	br := bufio.NewReader(c)
	// Greeting : VER, NMETHODS, METHODS...
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(br, hdr); err != nil || hdr[0] != 0x05 {
		c.Close()
		return
	}
	if _, err := io.ReadFull(br, make([]byte, int(hdr[1]))); err != nil {
		c.Close()
		return
	}
	c.Write([]byte{0x05, 0x00}) // no-auth

	// Request : VER CMD RSV ATYP ...
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil || req[1] != 0x01 { // CONNECT
		c.Write([]byte{0x05, 0x07, 0, 1, 0, 0, 0, 0, 0, 0})
		c.Close()
		return
	}
	var host string
	switch req[3] {
	case 0x01: // IPv4
		b := make([]byte, 4)
		io.ReadFull(br, b)
		host = net.IP(b).String()
	case 0x03: // domaine
		l := make([]byte, 1)
		io.ReadFull(br, l)
		b := make([]byte, int(l[0]))
		io.ReadFull(br, b)
		host = string(b)
	case 0x04: // IPv6
		b := make([]byte, 16)
		io.ReadFull(br, b)
		host = net.IP(b).String()
	default:
		c.Close()
		return
	}
	pb2 := make([]byte, 2)
	io.ReadFull(br, pb2)
	port := uint16(pb2[0])<<8 | uint16(pb2[1])

	if !s.openEgress(c, host, port) {
		c.Write([]byte{0x05, 0x01, 0, 1, 0, 0, 0, 0, 0, 0})
		c.Close()
		return
	}
	// Succès (BND.ADDR 0.0.0.0:0). La suite est relayée par EgressStream.
	c.Write([]byte{0x05, 0x00, 0, 1, 0, 0, 0, 0, 0, 0})
}

// --- HTTP CONNECT --------------------------------------------------------------

func (s *server) serveHTTP(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go s.handleConnect(c)
	}
}

func (s *server) handleConnect(c net.Conn) {
	br := bufio.NewReader(c)
	line, err := br.ReadString('\n')
	if err != nil {
		c.Close()
		return
	}
	parts := strings.Fields(line)
	if len(parts) < 2 || strings.ToUpper(parts[0]) != "CONNECT" {
		c.Write([]byte("HTTP/1.1 405 Method Not Allowed\r\n\r\n"))
		c.Close()
		return
	}
	host, portStr, err := net.SplitHostPort(parts[1])
	if err != nil {
		c.Close()
		return
	}
	port, _ := strconv.Atoi(portStr)
	// Consomme les en-têtes jusqu'à la ligne vide.
	for {
		l, e := br.ReadString('\n')
		if e != nil || l == "\r\n" || l == "\n" {
			break
		}
	}
	if !s.openEgress(c, host, uint16(port)) {
		c.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		c.Close()
		return
	}
	c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
}

// --- main ----------------------------------------------------------------------

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

	addr := envOr("STUB_ADDR", ":7443")
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Error("listen grpc", "err", err)
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

	srv := &server{log: log, pingSec: 30, pending: make(map[string]net.Conn)}
	grpcSrv := grpc.NewServer(opts...)
	pb.RegisterMiniMiniHubControlServer(grpcSrv, srv)

	// Écouteurs proxy (Phase 3) — clients SOCKS5 et HTTP-CONNECT.
	if a := envOr("SOCKS_ADDR", ":1080"); a != "" {
		if ln, err := net.Listen("tcp", a); err == nil {
			go srv.serveSocks(ln)
			log.Info("SOCKS5 proxy listening", "addr", a)
		} else {
			log.Error("listen socks", "err", err)
		}
	}
	if a := envOr("HTTP_ADDR", ":8080"); a != "" {
		if ln, err := net.Listen("tcp", a); err == nil {
			go srv.serveHTTP(ln)
			log.Info("HTTP-CONNECT proxy listening", "addr", a)
		} else {
			log.Error("listen http", "err", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() { <-ctx.Done(); log.Info("shutting down"); grpcSrv.GracefulStop() }()

	log.Info("parent-stub gRPC listening", "addr", addr)
	if err := grpcSrv.Serve(lis); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
