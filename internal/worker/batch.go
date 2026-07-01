package worker

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"
	"google.golang.org/protobuf/proto"

	"github.com/jmlmvi/miniminihub/internal/batch"
	"github.com/jmlmvi/miniminihub/internal/mop"
	"github.com/jmlmvi/miniminihub/internal/store"
	"github.com/jmlmvi/miniminihub/internal/tunnel"
	pb "github.com/jmlmvi/miniminihub/proto/mmhpb"
)

const (
	batchJobsBlobKey = "batch_jobs"    // config persistée (ConfigureBatchCommand)
	batchQueue       = "batch_reports" // file durable des rapports à remonter
	batchLeaseMs     = 60_000          // invisibilité d'un rapport en cours d'envoi
)

// BatchWorker (rôle "batch", V002 P5) : planifie des jobs (cron local), lance un
// agent IA pluggable, capture le rapport Markdown → file durable → remontée au mh.
// Piloté par ConfigureBatchCommand (poussé par le mh). Priorité 340.
type BatchWorker struct {
	log     *slog.Logger
	store   *store.Store
	tunnel  *tunnel.Client
	sub     <-chan any
	runners map[string]batch.AgentRunner

	mu      sync.Mutex
	cron    *cron.Cron
	jobs    map[string]*pb.JobSpec
	running map[string]*int32 // anti-overlap par job
}

func NewBatch() *BatchWorker { return &BatchWorker{} }

func (w *BatchWorker) Name() string       { return "batch" }
func (w *BatchWorker) StartPriority() int { return 340 }

func (w *BatchWorker) Init(_ context.Context, d mop.Deps) error {
	w.log = d.Log.With("worker", "batch")
	w.store = d.Store
	w.tunnel = d.Tunnel
	w.sub = d.Bus.Subscribe(TopicBatch)
	w.runners = batch.DefaultRunners()
	w.jobs = map[string]*pb.JobSpec{}
	w.running = map[string]*int32{}
	return nil
}

func (w *BatchWorker) Run(ctx context.Context) error {
	w.log.Info("batch worker ready", "runners", len(w.runners))
	// recharge la config persistée (survit au restart / déconnexion mh).
	if raw, _ := w.store.GetBlob(batchJobsBlobKey); len(raw) > 0 {
		var cmd pb.ConfigureBatchCommand
		if proto.Unmarshal(raw, &cmd) == nil {
			w.configure(ctx, &cmd, false)
		}
	}
	go w.drain(ctx) // consommateur : remonte les rapports au mh

	for {
		select {
		case <-ctx.Done():
			w.mu.Lock()
			if w.cron != nil {
				w.cron.Stop()
			}
			w.mu.Unlock()
			return nil
		case msg := <-w.sub:
			if cmd, ok := msg.(*pb.ConfigureBatchCommand); ok {
				w.configure(ctx, cmd, true)
			}
		}
	}
}

// configure remplace INTÉGRALEMENT la config (déclaratif) : persiste + réarme le cron.
func (w *BatchWorker) configure(ctx context.Context, cmd *pb.ConfigureBatchCommand, persist bool) {
	if persist {
		if raw, err := proto.Marshal(cmd); err == nil {
			_ = w.store.PutBlob(batchJobsBlobKey, raw)
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cron != nil {
		w.cron.Stop()
	}
	w.cron = cron.New()
	w.jobs = map[string]*pb.JobSpec{}
	w.running = map[string]*int32{}
	armed := 0
	for _, j := range cmd.Jobs {
		w.jobs[j.Id] = j
		var flag int32
		w.running[j.Id] = &flag
		if !j.Enabled || j.Schedule == "" {
			continue
		}
		spec := j.Schedule
		if j.Timezone != "" {
			spec = "CRON_TZ=" + j.Timezone + " " + spec
		}
		job := j // capture
		if _, err := w.cron.AddFunc(spec, func() { w.fire(ctx, job) }); err != nil {
			w.log.Error("cron invalide", "job", j.Id, "schedule", j.Schedule, "err", err)
			continue
		}
		armed++
	}
	w.cron.Start()
	w.log.Info("config batch appliquée", "jobs", len(cmd.Jobs), "armés", armed)
}

// RunNow déclenche un job immédiatement (utilisé par l'action run_now, hors planning).
func (w *BatchWorker) RunNow(ctx context.Context, jobID string) bool {
	w.mu.Lock()
	j := w.jobs[jobID]
	w.mu.Unlock()
	if j == nil {
		return false
	}
	go w.fire(ctx, j)
	return true
}

// fire exécute un run (anti-overlap "skip") : résout le prompt, lance le runner,
// enfile le rapport pour remontée.
func (w *BatchWorker) fire(ctx context.Context, j *pb.JobSpec) {
	w.mu.Lock()
	flag := w.running[j.Id]
	w.mu.Unlock()
	if flag == nil {
		return
	}
	if !atomic.CompareAndSwapInt32(flag, 0, 1) {
		w.log.Warn("run précédent encore actif → skip", "job", j.Id)
		_, _ = w.store.Incr("batch_" + j.Id + "_skipped")
		w.enqueueReport(j, batch.RunResult{Status: "skipped", StartedMs: time.Now().UnixMilli(), EndedMs: time.Now().UnixMilli(), Metrics: map[string]string{}})
		return
	}
	defer atomic.StoreInt32(flag, 0)

	runID := newRunID(w.store)
	log := w.log.With("job", j.Id, "run_id", runID)
	runner := w.runners[j.AgentKind]
	if runner == nil {
		log.Error("runner inconnu", "kind", j.AgentKind)
		w.enqueueReport(j, batch.RunResult{Status: "failed", Error: "runner inconnu: " + j.AgentKind, Metrics: map[string]string{}})
		return
	}
	prompt, err := batch.ResolvePrompt(j.PromptTemplate, j.PromptVars, time.Now())
	if err != nil {
		log.Error("résolution prompt", "err", err)
		w.enqueueReport(j, batch.RunResult{Status: "failed", Error: err.Error(), Metrics: map[string]string{}})
		return
	}
	timeout := time.Duration(j.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	log.Info("run agent", "kind", j.AgentKind, "timeout", timeout)
	res := runner.Run(ctx, prompt, j.AgentParams, timeout)
	log.Info("run terminé", "status", res.Status, "md_bytes", len(res.ReportMD))
	w.enqueueReportWithID(j, runID, res)
}

func (w *BatchWorker) enqueueReport(j *pb.JobSpec, res batch.RunResult) {
	w.enqueueReportWithID(j, newRunID(w.store), res)
}

func (w *BatchWorker) enqueueReportWithID(j *pb.JobSpec, runID string, res batch.RunResult) {
	rep := &pb.BatchReport{
		JobId:       j.Id,
		RunId:       runID,
		Status:      res.Status,
		ReportMd:    res.ReportMD,
		Metrics:     res.Metrics,
		StartedAtMs: res.StartedMs,
		EndedAtMs:   res.EndedMs,
		Error:       res.Error,
		ReportHubs:  j.ReportHubs,
	}
	raw, err := proto.Marshal(rep)
	if err != nil {
		w.log.Error("marshal report", "err", err)
		return
	}
	if _, err := w.store.QueueEnqueue(batchQueue, raw, time.Now().UnixMilli()); err != nil {
		w.log.Error("enqueue report", "err", err)
	}
}

// drain remonte les rapports en file au mh (at-least-once). Ack sur succès,
// nack (backoff) sinon. Survit aux coupures : la file est durable (bbolt).
func (w *BatchWorker) drain(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().UnixMilli()
			items, err := w.store.QueueLease(batchQueue, 5, batchLeaseMs, now)
			if err != nil || len(items) == 0 {
				continue
			}
			for _, it := range items {
				var rep pb.BatchReport
				if proto.Unmarshal(it.Payload, &rep) != nil {
					_ = w.store.QueueAck(batchQueue, it.ID) // payload corrompu → drop
					continue
				}
				if err := w.tunnel.PushBatchReport(ctx, &rep); err != nil {
					att, kept, _ := w.store.QueueNack(batchQueue, it.ID, 15_000, time.Now().UnixMilli(), 20)
					w.log.Warn("remontée rapport échouée → retry", "job", rep.JobId, "attempt", att, "kept", kept, "err", err)
				} else {
					_ = w.store.QueueAck(batchQueue, it.ID)
					n, _ := w.store.Incr("batch_reports_sent")
					w.log.Info("rapport remonté au mh", "job", rep.JobId, "run_id", rep.RunId, "total", n)
				}
			}
		}
	}
}

func newRunID(s *store.Store) string {
	n, _ := s.Incr("batch_run_seq")
	return "run-" + time.Now().UTC().Format("20060102-150405") + "-" + itoa(n)
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
