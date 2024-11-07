package backend

import (
	"context"
	"sync"
)

func NewMemoryPageCache() PageCache {
	return &memoryPageCache{}
}

type memoryPageCache struct {
	m sync.Mutex

	pages []Page
}

func (m *memoryPageCache) Create(_ context.Context) (Page, error) {
	m.m.Lock()
	defer m.m.Unlock()

	newPage := newMemoryPage()
	m.pages = append(m.pages, newPage)
	return newPage, nil
}

func (m *memoryPageCache) Delete(_ context.Context, page Page) error {
	m.m.Lock()
	defer m.m.Unlock()

	for i, p := range m.pages {
		if p == page {
			m.pages = append(m.pages[:i], m.pages[i+1:]...)
			break
		}
	}

	return nil
}

func (m *memoryPageCache) List(_ context.Context) ([]Page, error) {
	m.m.Lock()
	defer m.m.Unlock()

	retList := make([]Page, len(m.pages))
	copy(retList, m.pages)
	return retList, nil
}
