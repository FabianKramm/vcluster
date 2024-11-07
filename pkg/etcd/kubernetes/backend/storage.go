package backend

import (
	"container/heap"
	"context"
	"fmt"
	"sync"

	"github.com/loft-sh/vcluster/pkg/etcd/kubernetes/server"
)

const MaxPageSize = 250 * 1024 // 250KB

type Storage interface {
	Start(ctx context.Context) error
	List(ctx context.Context) ([]*server.Event, error)
	Insert(ctx context.Context, row *server.Event) error
	Delete(ctx context.Context, row *server.Event) error
	DbSize(ctx context.Context) int64
}

func NewStorage(factory PageCache) Storage {
	return &storage{
		pageSizeHeap: &pageSizeHeap{},
		byRow:        map[int64]Page{},

		factory: factory,
	}
}

type storage struct {
	m sync.RWMutex

	pageSizeHeap *pageSizeHeap
	byRow        map[int64]Page

	factory PageCache
}

func (s *storage) Start(ctx context.Context) error {
	// list all pages
	pages, err := s.factory.List(ctx)
	if err != nil {
		return fmt.Errorf("list pages: %w", err)
	}

	s.m.Lock()
	defer s.m.Unlock()

	// register all pages
	for _, page := range pages {
		err = s.registerPage(page)
		if err != nil {
			return fmt.Errorf("register page: %w", err)
		}
	}

	return nil
}

func (s *storage) List(_ context.Context) ([]*server.Event, error) {
	s.m.RLock()
	defer s.m.RUnlock()

	retRows := make([]*server.Event, 0, len(s.byRow))
	for _, page := range s.pageSizeHeap.arr {
		rows, err := page.Rows()
		if err != nil {
			return nil, fmt.Errorf("get rows from page: %w", err)
		}

		retRows = append(retRows, rows...)
	}

	return retRows, nil
}

func (s *storage) Insert(ctx context.Context, row *server.Event) error {
	s.m.Lock()
	defer s.m.Unlock()

	var err error

	// peak at the heap
	page := s.pageSizeHeap.Peek()
	if page == nil {
		return s.insertIntoNewPage(ctx, row)
	}

	// check new size
	newSize, err := page.SizeWithRow(row)
	if err != nil {
		return fmt.Errorf("get new size: %w", err)
	} else if newSize > MaxPageSize {
		return s.insertIntoNewPage(ctx, row)
	}

	// pop the one with the biggest size
	page = s.pageSizeHeap.Pop()
	err = page.Insert(ctx, row)
	if err != nil {
		return fmt.Errorf("insert into page: %w", err)
	}

	s.pageSizeHeap.Push(page)
	s.byRow[row.KV.ModRevision] = page
	return nil
}

func (s *storage) Delete(ctx context.Context, row *server.Event) error {
	s.m.Lock()
	defer s.m.Unlock()

	page, ok := s.byRow[row.KV.ModRevision]
	if !ok {
		return nil
	}

	err := page.Delete(ctx, row)
	if err != nil {
		return fmt.Errorf("delete from page: %w", err)
	}
	delete(s.byRow, row.KV.ModRevision)

	// should delete page?
	if page.Size() == 0 {
		err = s.factory.Delete(ctx, page)
		if err != nil {
			return fmt.Errorf("delete page: %w", err)
		}

		s.pageSizeHeap.Delete(page)
	}

	return nil
}

func (s *storage) insertIntoNewPage(ctx context.Context, row *server.Event) error {
	minPage, err := s.factory.Create(ctx)
	if err != nil {
		return fmt.Errorf("new page: %w", err)
	}

	newSize, err := minPage.SizeWithRow(row)
	if err != nil {
		return fmt.Errorf("calculate size: %w", err)
	} else if newSize > MaxPageSize {
		return fmt.Errorf("row %s is too big to store (%d)", row.KV.Key, newSize)
	}

	err = minPage.Insert(ctx, row)
	if err != nil {
		return fmt.Errorf("insert row: %w", err)
	}

	err = s.registerPage(minPage)
	if err != nil {
		return fmt.Errorf("register page: %w", err)
	}

	return nil
}

func (s *storage) registerPage(page Page) error {
	s.pageSizeHeap.Push(page)
	rows, err := page.Rows()
	if err != nil {
		return err
	}
	for _, row := range rows {
		s.byRow[row.KV.ModRevision] = page
	}

	return nil
}

func (s *storage) DbSize(_ context.Context) int64 {
	s.m.RLock()
	defer s.m.RUnlock()

	totalSize := int64(0)
	for _, page := range s.pageSizeHeap.arr {
		totalSize += int64(page.Size())
	}

	return totalSize
}

type pageSizeHeap struct {
	m sync.Mutex

	arr pageHeap
}

func (p *pageSizeHeap) Peek() Page {
	p.m.Lock()
	defer p.m.Unlock()

	if len(p.arr) == 0 {
		return nil
	}
	return p.arr[0]
}

func (p *pageSizeHeap) Pop() Page {
	p.m.Lock()
	defer p.m.Unlock()

	if len(p.arr) == 0 {
		return nil
	}
	return heap.Pop(&p.arr).(Page)
}

func (p *pageSizeHeap) Push(page Page) {
	p.m.Lock()
	defer p.m.Unlock()

	heap.Push(&p.arr, page)
}

func (p *pageSizeHeap) Delete(pageToDelete Page) {
	p.m.Lock()
	defer p.m.Unlock()

	for i, page := range p.arr {
		if page == pageToDelete {
			p.arr = append(p.arr[:i], p.arr[i+1:]...)
			break
		}
	}
}

type pageHeap []Page

func (pq pageHeap) Len() int {
	return len(pq)
}

func (pq pageHeap) Less(i, j int) bool {
	return pq[i].Size() < pq[j].Size()
}

func (pq pageHeap) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
}

func (pq *pageHeap) Push(x interface{}) {
	item := x.(Page)
	*pq = append(*pq, item)
}

func (pq *pageHeap) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil // avoid memory leak
	*pq = old[0 : n-1]
	return item
}
