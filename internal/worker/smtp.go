package worker

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jmlmvi/miniminihub/internal/mop"
	"github.com/jmlmvi/miniminihub/internal/store"
	"github.com/jmlmvi/miniminihub/internal/tunnel"
	pb "github.com/jmlmvi/miniminihub/proto/mmhpb"
)

// SmtpWorker = rôle "smtp" (D-18) : remise MX directe (port 25) d'un message,
// STARTTLS opportuniste (D-09). Reçoit les SmtpSendCommand via le bus, effectue
// la remise puis remonte un SmtpResult au parent (PushResult). Priorité 320.
type SmtpWorker struct {
	log    *slog.Logger
	store  *store.Store
	tunnel *tunnel.Client
	sub    <-chan any
	helo   string
}

// NewSmtp construit le worker smtp.
func NewSmtp() *SmtpWorker { return &SmtpWorker{} }

func (w *SmtpWorker) Name() string       { return "smtp" }
func (w *SmtpWorker) StartPriority() int { return 320 }

func (w *SmtpWorker) Init(_ context.Context, d mop.Deps) error {
	w.log = d.Log.With("worker", "smtp")
	w.store = d.Store
	w.tunnel = d.Tunnel
	w.sub = d.Bus.Subscribe(TopicSmtp) // abonnement dans Init (règle Socle)
	if h, err := os.Hostname(); err == nil && h != "" {
		w.helo = h
	} else {
		w.helo = "mmh"
	}
	return nil
}

func (w *SmtpWorker) Run(ctx context.Context) error {
	w.log.Info("smtp worker ready (MX direct)")
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-w.sub:
			cmd, ok := msg.(*pb.SmtpSendCommand)
			if !ok {
				continue
			}
			go w.handle(ctx, cmd)
		}
	}
}

// handle remet le message par MX direct et remonte le résultat.
func (w *SmtpWorker) handle(ctx context.Context, cmd *pb.SmtpSendCommand) {
	log := w.log.With("request_id", cmd.RequestId, "from", cmd.MailFrom, "rcpts", len(cmd.RcptTo))
	res := tunnel.SmtpResultData{RequestID: cmd.RequestId}

	helo := cmd.Helo
	if helo == "" {
		helo = w.helo
	}

	// Tous les destinataires doivent partager le même domaine (une remise = un MX).
	domain, err := singleDomain(cmd.RcptTo)
	if err != nil {
		res.Status, res.Error = "FAILED", err.Error()
		w.report(ctx, log, res)
		return
	}

	mxHosts, err := resolveMX(domain)
	if err != nil || len(mxHosts) == 0 {
		res.Status = "FAILED"
		res.Error = fmt.Sprintf("résolution MX %s: %v", domain, err)
		w.report(ctx, log, res)
		return
	}

	// Essaie chaque MX par préférence croissante ; DEFERRED si tous injoignables.
	var lastErr error
	for _, mx := range mxHosts {
		code, smtpMsg, derr := deliver(mx, helo, cmd.MailFrom, cmd.RcptTo, cmd.Data, cmd.Starttls)
		res.MXHost = mx
		res.SMTPCode = code
		res.SMTPMessage = smtpMsg
		if derr == nil {
			res.Accepted = true
			res.Status = "SENT"
			n, _ := w.store.Incr("smtp_sent")
			log.Info("smtp remis", "mx", mx, "code", code, "total_sent", n)
			w.report(ctx, log, res)
			return
		}
		lastErr = derr
		log.Warn("smtp MX échec, essai suivant", "mx", mx, "err", derr)
	}
	// Aucun MX n'a accepté : rejet permanent (5xx) = FAILED, sinon DEFERRED.
	res.Error = fmt.Sprintf("aucun MX joignable/acceptant: %v", lastErr)
	if isPermanent(res.SMTPCode) {
		res.Status = "FAILED"
	} else {
		res.Status = "DEFERRED"
	}
	w.report(ctx, log, res)
}

func (w *SmtpWorker) report(ctx context.Context, log *slog.Logger, res tunnel.SmtpResultData) {
	if err := w.tunnel.PushSmtpResult(ctx, res); err != nil {
		log.Error("remontée résultat SMTP", "err", err)
	}
}

// deliver ouvre une session SMTP vers mx:25 et remet le message. Renvoie le
// dernier code/msg SMTP et une erreur non nulle si la remise a échoué.
func deliver(mx, helo, from string, rcpts []string, data []byte, starttls bool) (code, msg string, err error) {
	addr := net.JoinHostPort(mx, "25")
	conn, err := net.DialTimeout("tcp", addr, 20*time.Second)
	if err != nil {
		return "", "", fmt.Errorf("dial %s: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(90 * time.Second))

	c, err := smtp.NewClient(conn, mx)
	if err != nil {
		conn.Close()
		return "", "", fmt.Errorf("smtp client %s: %w", mx, err)
	}
	defer c.Close()

	if err := c.Hello(helo); err != nil {
		return smtpErr(err)
	}
	// STARTTLS opportuniste (D-09) : on tente si annoncé, sans vérifier le cert
	// (les MX présentent souvent un cert ne correspondant pas au nom résolu).
	if starttls {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: mx, InsecureSkipVerify: true}); err != nil {
				return smtpErr(fmt.Errorf("starttls: %w", err))
			}
		}
	}
	if err := c.Mail(from); err != nil {
		return smtpErr(err)
	}
	for _, r := range rcpts {
		if err := c.Rcpt(r); err != nil {
			return smtpErr(err)
		}
	}
	wr, err := c.Data()
	if err != nil {
		return smtpErr(err)
	}
	if _, err := wr.Write(data); err != nil {
		return smtpErr(err)
	}
	if err := wr.Close(); err != nil {
		// La fermeture du DATA porte la réponse finale du MX (250 accepté / 5xx rejet).
		return smtpErr(err)
	}
	_ = c.Quit()
	return "250", "accepted", nil
}

// smtpErr extrait le code SMTP d'une *smtp.textproto error si possible.
func smtpErr(err error) (string, string, error) {
	if err == nil {
		return "", "", nil
	}
	// net/textproto.Error a les champs Code/Msg ; on parse le texte "code msg".
	s := err.Error()
	code := ""
	if len(s) >= 3 && s[0] >= '0' && s[0] <= '9' {
		code = s[:3]
	}
	return code, s, err
}

// resolveMX renvoie les hôtes MX d'un domaine triés par préférence croissante.
// Fallback A/AAAA (le domaine lui-même) si aucun MX (RFC 5321 §5.1).
func resolveMX(domain string) ([]string, error) {
	mxs, err := net.LookupMX(domain)
	if err == nil && len(mxs) > 0 {
		sort.SliceStable(mxs, func(i, j int) bool { return mxs[i].Pref < mxs[j].Pref })
		hosts := make([]string, 0, len(mxs))
		for _, mx := range mxs {
			hosts = append(hosts, strings.TrimSuffix(mx.Host, "."))
		}
		return hosts, nil
	}
	// Pas de MX → implicite : le domaine lui-même (si résoluble).
	if _, aerr := net.LookupHost(domain); aerr == nil {
		return []string{domain}, nil
	}
	if err == nil {
		err = fmt.Errorf("aucun MX ni A/AAAA")
	}
	return nil, err
}

// singleDomain vérifie que tous les destinataires partagent le même domaine.
func singleDomain(rcpts []string) (string, error) {
	if len(rcpts) == 0 {
		return "", fmt.Errorf("aucun destinataire")
	}
	var domain string
	for _, r := range rcpts {
		at := strings.LastIndex(r, "@")
		if at < 0 || at == len(r)-1 {
			return "", fmt.Errorf("destinataire invalide: %q", r)
		}
		d := strings.ToLower(r[at+1:])
		if domain == "" {
			domain = d
		} else if d != domain {
			return "", fmt.Errorf("destinataires multi-domaines non supportés (%s vs %s)", domain, d)
		}
	}
	return domain, nil
}

// isPermanent : un code 5xx est un rejet définitif (FAILED), 4xx/absent = DEFERRED.
func isPermanent(code string) bool {
	return strings.HasPrefix(code, "5")
}
