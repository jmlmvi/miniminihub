// Package tunnel gère le canal gRPC sortant vers le minihub parent
// (dial, heartbeat, pollcommand). Phase 0 : plaintext (D-20).
package tunnel

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/jmlmvi/miniminihub/proto/mmhpb"
)

// Client = connexion gRPC vers le parent + stub de contrôle.
type Client struct {
	endpoint string
	id       string
	slug     string
	log      *slog.Logger

	conn *grpc.ClientConn
	ctrl pb.MiniMiniHubControlClient
}

// New prépare un client (ne se connecte pas encore).
func New(endpoint, id, slug string, log *slog.Logger) *Client {
	return &Client{
		endpoint: endpoint,
		id:       id,
		slug:     slug,
		log:      log.With("component", "tunnel"),
	}
}

// Connect ouvre le canal gRPC sortant (plaintext en Phase 0).
func (c *Client) Connect(ctx context.Context) error {
	conn, err := grpc.NewClient(c.endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("dial %s: %w", c.endpoint, err)
	}
	c.conn = conn
	c.ctrl = pb.NewMiniMiniHubControlClient(conn)
	c.log.Info("tunnel connected", "endpoint", c.endpoint)
	return nil
}

// Close ferme le canal.
func (c *Client) Close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// Heartbeat envoie un battement unaire avec timeout dérivé du context (R11).
func (c *Client) Heartbeat(ctx context.Context, seq uint64) (*pb.HeartbeatResponse, error) {
	hbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	resp, err := c.ctrl.Heartbeat(hbCtx, &pb.HeartbeatRequest{
		MiniminihubId: c.id,
		Slug:          c.slug,
		ClientTsMs:    time.Now().UnixMilli(),
		Sequence:      seq,
	})
	if err != nil {
		return nil, fmt.Errorf("heartbeat seq=%d: %w", seq, err)
	}
	return resp, nil
}

// Poll ouvre la server-stream PollCommand et invoque onCommand pour chaque
// commande poussée par le parent. Bloque jusqu'à fin de stream / erreur / ctx.
func (c *Client) Poll(ctx context.Context, onCommand func(*pb.Command)) error {
	stream, err := c.ctrl.PollCommand(ctx, &pb.PollRequest{
		MiniminihubId: c.id,
		Slug:          c.slug,
	})
	if err != nil {
		return fmt.Errorf("open pollcommand: %w", err)
	}
	c.log.Info("pollcommand stream opened")

	for {
		cmd, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("pollcommand recv: %w", err) // D-13 : déclenche reconnexion
		}
		onCommand(cmd)
	}
}
