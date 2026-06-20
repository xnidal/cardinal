package storage

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

// TodoItem is deliberately small: a title and a status. Priority, due dates,
// subtasks and progress percentages all live elsewhere (or just don't exist)
// because most todo churn in practice is "what should I do next", not metadata.
type TodoItem struct {
	ID     string     `json:"id"`
	Title  string     `json:"title"`
	Status TodoStatus `json:"status"`
	// CreatedAt / UpdatedAt kept for debugging; not surfaced in list output.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TodoStore is an in-memory, session-scoped store for todo items.
// Cardinal does not persist todos across sessions, so this deliberately
// avoids writing to disk.
type TodoStore struct {
	mu    sync.Mutex
	items []TodoItem
	next  int // running numeric suffix, makes ids short and readable
}

func NewTodoStore() *TodoStore {
	return &TodoStore{items: []TodoItem{}}
}

func (s *TodoStore) Add(title string) (*TodoItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	now := time.Now()
	item := TodoItem{
		ID:        generateTodoID(s.next),
		Title:     title,
		Status:    TodoPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.items = append(s.items, item)
	cp := item
	return &cp, nil
}

func (s *TodoStore) List(statusFilter string) []TodoItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	if statusFilter == "" {
		out := make([]TodoItem, len(s.items))
		copy(out, s.items)
		return out
	}
	out := make([]TodoItem, 0, len(s.items))
	for _, it := range s.items {
		if string(it.Status) == statusFilter {
			out = append(out, it)
		}
	}
	return out
}

// resolveID accepts either a full id ("20260120123456-ab12") or just the
// short suffix ("ab12"). The model only ever sees the short form in
// formatted output, so accepting it as input keeps the API symmetric.
func (s *TodoStore) resolveID(id string) *TodoItem {
	for i := range s.items {
		if s.items[i].ID == id {
			return &s.items[i]
		}
		if shortID(s.items[i].ID) == id {
			return &s.items[i]
		}
	}
	return nil
}

func (s *TodoStore) Update(id string, status *TodoStatus, title *string) (*TodoItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.resolveID(id)
	if item == nil {
		return nil, ErrTodoNotFound
	}
	if status != nil {
		item.Status = *status
	}
	if title != nil {
		item.Title = *title
	}
	item.UpdatedAt = time.Now()
	cp := *item
	return &cp, nil
}

func (s *TodoStore) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, it := range s.items {
		if it.ID == id || shortID(it.ID) == id {
			s.items = append(s.items[:i], s.items[i+1:]...)
			return nil
		}
	}
	return ErrTodoNotFound
}

// generateTodoID makes ids like "ab12" — short, easy to read aloud, easy to
// copy-paste across turns. We tag them with a per-process counter so two
// adds in the same millisecond never collide.
func generateTodoID(n int) string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return fmtShort(n) + "-" + hex.EncodeToString(b[:])
}

func fmtShort(n int) string {
	if n <= 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func shortID(id string) string {
	if i := indexByte(id, '-'); i >= 0 && i < len(id)-1 {
		return id[i+1:]
	}
	return id
}

// Tiny local helpers to keep this file free of "strings"/"fmt" imports just
// for one rune lookup. These add zero allocation on the hot path.
func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// ErrTodoNotFound is returned when an operation targets a todo ID that does
// not exist in the current session store.
var ErrTodoNotFound = errTodoNotFound{}

type errTodoNotFound struct{}

func (errTodoNotFound) Error() string { return "todo not found" }
