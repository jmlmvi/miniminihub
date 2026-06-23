package worker

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/jmlmvi/miniminihub/internal/mop"
	"github.com/jmlmvi/miniminihub/internal/store"
	"github.com/jmlmvi/miniminihub/internal/tunnel"
	pb "github.com/jmlmvi/miniminihub/proto/mmhpb"
)

// ProxyWorker = rôle exit node (D-17). Reçoit les ordres d'égress (host:port)
// via le bus, ouvre la connexion sortante depuis l'IP de la VM, et relaie les
// bytes dans un flux gRPC bidi (EgressStream). Priorité 300, actif si role "proxy".
type ProxyWorker struct {
	log    *slog.Logger
	store  *store.Store
	tunnel *tunnel.Client
	sub    <-chan any
}

// NewProxy construit le worker proxy.
func NewProxy() *ProxyWorker { return &ProxyWorker{} }

func (w *ProxyWorker) Name() string       { return "proxy" }
func (w *ProxyWorker) StartPriority() int { return 300 }

func (w *ProxyWorker) Init(_ context.Context, d mop.Deps) error {
	w.log = d.Log.With("worker", "proxy")
	w.store = d.Store
	w.tunnel = d.Tunnel
	w.sub = d.Bus.Subscribe(TopicEgress) // abonnement dans Init (règle Socle)
	return nil
}

// Run consomme les demandes d'égress et lance un relais par connexion.
func (w *ProxyWorker) Run(ctx context.Context) error {
	w.log.Info("proxy worker ready (exit node)")
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-w.sub:
			cmd, ok := msg.(*pb.EgressOpenCommand)
			if !ok {
				continue
			}
			go w.handle(ctx, cmd)
		}
	}
}

// handle ouvre la sortie vers host:port et relaie le trafic via EgressStream.
func (w *ProxyWorker) handle(ctx context.Context, cmd *pb.EgressOpenCommand) {
	target := net.JoinHostPort(cmd.Host, strconv.Itoa(int(cmd.Port)))
	log := w.log.With("conn_id", cmd.ConnId, "target", target)

	stream, err := w.tunnel.EgressStream(ctx)
	if err != nil {
		log.Error("open egress stream", "err", err)
		return
	}
	// 1er frame : annonce le conn_id au parent.
	if err := stream.Send(&pb.EgressFrame{ConnId: cmd.ConnId}); err != nil {
		log.Error("announce conn_id", "err", err)
		return
	}

	conn, err := net.DialTimeout("tcp", target, 15*time.Second)
	if err != nil {
		log.Warn("dial target failed", "err", err)
		_ = stream.Send(&pb.EgressFrame{Close: true, Error: err.Error()})
		_ = stream.CloseSend()
		return
	}
	defer conn.Close()
	n, _ := w.store.Incr("egress_connections")
	log.Info("egress established", "total_egress", n)

	// target -> stream (seul émetteur après l'annonce).
	go func() {
		buf := make([]byte, 32*1024)
		for {
			nr, rerr := conn.Read(buf)
			if nr > 0 {
				if serr := stream.Send(&pb.EgressFrame{Data: buf[:nr]}); serr != nil {
					return
				}
			}
			if rerr != nil {
				_ = stream.Send(&pb.EgressFrame{Close: true})
				return
			}
		}
	}()

	// stream -> target.
	for {
		f, rerr := stream.Recv()
		if rerr != nil {
			return
		}
		if len(f.Data) > 0 {
			if _, werr := conn.Write(f.Data); werr != nil {
				return
			}
		}
		if f.Close {
			return
		}
	}
}
