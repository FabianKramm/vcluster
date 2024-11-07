package backend

import (
	"context"
	"encoding/base64"
	"maps"
	"sync"

	"github.com/loft-sh/vcluster/pkg/etcd/kubernetes/server"
)

func newMemoryPage() Page {
	return &memoryPage{
		rows: map[int64]*server.Event{},
	}
}

type memoryPage struct {
	m sync.RWMutex

	rows map[int64]*server.Event
}

func (m *memoryPage) Size() int {
	m.m.RLock()
	defer m.m.RUnlock()

	bytes, _ := encode(m.rows)
	return len(base64.StdEncoding.EncodeToString(bytes))
}

func (m *memoryPage) SizeWithRow(e *server.Event) (int, error) {
	m.m.RLock()
	defer m.m.RUnlock()

	clonedMaps := maps.Clone(m.rows)
	clonedMaps[e.KV.ModRevision] = e
	bytes, _ := encode(clonedMaps)
	return len(base64.StdEncoding.EncodeToString(bytes)), nil
}

func (m *memoryPage) Rows() ([]*server.Event, error) {
	m.m.RLock()
	defer m.m.RUnlock()

	retRows := make([]*server.Event, 0, len(m.rows))
	for _, row := range m.rows {
		retRows = append(retRows, row)
	}

	return retRows, nil
}

func (m *memoryPage) Insert(_ context.Context, row *server.Event) error {
	m.m.Lock()
	defer m.m.Unlock()

	m.rows[row.KV.ModRevision] = row
	return nil
}

func (m *memoryPage) Delete(_ context.Context, row *server.Event) error {
	m.m.Lock()
	defer m.m.Unlock()

	delete(m.rows, row.KV.ModRevision)
	return nil
}
