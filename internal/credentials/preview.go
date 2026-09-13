package credentials

import (
	"crypto/rand"
	"errors"
	"sync"
	"time"
)

const PreviewTTL = 5 * time.Minute

type previewTicket struct {
	entries []ImportEntry
	timer   *time.Timer
}
type PreviewStore struct {
	mu     sync.Mutex
	items  map[string]previewTicket
	closed bool
}

func (s *PreviewStore) Add(entries []ImportEntry) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || len(s.items) >= 4 {
		return "", errors.New("close an existing import preview before opening another")
	}
	if s.items == nil {
		s.items = map[string]previewTicket{}
	}
	id := rand.Text()
	s.items[id] = previewTicket{entries: entries, timer: time.AfterFunc(PreviewTTL, func() { s.Drop(id) })}
	return id, nil
}
func (s *PreviewStore) Take(id string) ([]ImportEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket, ok := s.items[id]
	if !ok {
		return nil, errors.New("import preview expired or configuration changed; preview the file again")
	}
	ticket.timer.Stop()
	delete(s.items, id)
	return ticket.entries, nil
}
func (s *PreviewStore) Drop(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ticket, ok := s.items[id]; ok {
		ticket.timer.Stop()
		delete(s.items, id)
	}
}
func (s *PreviewStore) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for id, ticket := range s.items {
		ticket.timer.Stop()
		delete(s.items, id)
	}
}
