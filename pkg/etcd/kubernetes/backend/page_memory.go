package backend

import (
	"context"
	"encoding/base64"
	"maps"
	"sync"
)

func newMemoryPage() Page {
	return &memoryPage{
		rows: map[int64]*Event{},
	}
}

type memoryPage struct {
	m sync.RWMutex

	rows map[int64]*Event
}

func (m *memoryPage) Size() int {
	m.m.RLock()
	defer m.m.RUnlock()

	bytes, _ := encode(m.rows)
	return len(base64.StdEncoding.EncodeToString(bytes))
}

func (m *memoryPage) SizeWithRow(e *Event) (int, error) {
	m.m.RLock()
	defer m.m.RUnlock()

	clonedMaps := maps.Clone(m.rows)
	clonedMaps[e.Server.KV.ModRevision] = e
	bytes, _ := encode(clonedMaps)
	return len(base64.StdEncoding.EncodeToString(bytes)), nil
}

func (m *memoryPage) Rows() ([]*Event, error) {
	m.m.RLock()
	defer m.m.RUnlock()

	retRows := make([]*Event, 0, len(m.rows))
	for _, row := range m.rows {
		retRows = append(retRows, row)
	}

	return retRows, nil
}

func (m *memoryPage) Insert(_ context.Context, row *Event) error {
	m.m.Lock()
	defer m.m.Unlock()

	m.rows[row.Server.KV.ModRevision] = row
	return nil
}

func (m *memoryPage) Delete(_ context.Context, revisions ...int64) error {
	m.m.Lock()
	defer m.m.Unlock()

	for _, revision := range revisions {
		delete(m.rows, revision)
	}
	return nil
}
