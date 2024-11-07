package backend

import (
	"context"
	"sync"

	"github.com/loft-sh/vcluster/pkg/etcd/kubernetes/server"
)

type broadcaster struct {
	sync.Mutex

	subs map[chan []*server.Event]struct{}
}

func (b *broadcaster) Subscribe(ctx context.Context) <-chan []*server.Event {
	b.Lock()
	defer b.Unlock()

	sub := make(chan []*server.Event, 100)
	if b.subs == nil {
		b.subs = map[chan []*server.Event]struct{}{}
	}
	b.subs[sub] = struct{}{}
	go func() {
		<-ctx.Done()
		b.unsub(sub, true)
	}()

	return sub
}

func (b *broadcaster) Stream(item []*server.Event) {
	b.Lock()
	for sub := range b.subs {
		select {
		case sub <- item:
		default:
			// Slow consumer, drop
			go b.unsub(sub, true)
		}
	}
	b.Unlock()
}

func (b *broadcaster) unsub(sub chan []*server.Event, lock bool) {
	if lock {
		b.Lock()
	}
	if _, ok := b.subs[sub]; ok {
		close(sub)
		delete(b.subs, sub)
	}
	if lock {
		b.Unlock()
	}
}
