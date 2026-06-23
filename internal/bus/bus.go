// Package bus est le KvBus interne (pub/sub sur channels) du miniMiniHub.
// Abonnement uniquement dans Worker.Init() (règle reprise du Socle V005).
package bus

import "sync"

// Bus = fan-out non bloquant par topic.
type Bus struct {
	mu   sync.RWMutex
	subs map[string][]chan any
}

// New construit un bus vide.
func New() *Bus { return &Bus{subs: make(map[string][]chan any)} }

// Subscribe retourne un channel recevant les messages publiés sur topic.
func (b *Bus) Subscribe(topic string) <-chan any {
	ch := make(chan any, 128)
	b.mu.Lock()
	b.subs[topic] = append(b.subs[topic], ch)
	b.mu.Unlock()
	return ch
}

// Publish diffuse msg à tous les abonnés du topic (drop si un abonné sature).
func (b *Bus) Publish(topic string, msg any) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs[topic] {
		select {
		case ch <- msg:
		default:
		}
	}
}
