package env

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Item struct {
	Key      string
	Value    string
	Modified bool
	Deleted  bool
}

type Store struct {
	mu       sync.RWMutex
	order    []string        // stable key order
	items    map[string]Item // current items
	filtered []string        // keys matching filter
	query    string
	dirty    bool
}

func NewStore() *Store {
	s := &Store{
		items: make(map[string]Item),
	}
	s.LoadFromProcess()
	return s
}

func (s *Store) LoadFromProcess() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.order = s.order[:0]
	s.items = make(map[string]Item)
	env := os.Environ()
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		key := parts[0]
		val := ""
		if len(parts) > 1 {
			val = parts[1]
		}
		s.items[key] = Item{Key: key, Value: val}
		s.order = append(s.order, key)
	}
	sort.Strings(s.order)
	s.filtered = append([]string{}, s.order...)
	s.query = ""
	s.dirty = false
}

func (s *Store) ListKeys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string{}, s.filtered...)
}

func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.filtered)
}

func (s *Store) GetByIndex(idx int) (Item, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if idx < 0 || idx >= len(s.filtered) {
		return Item{}, false
	}
	key := s.filtered[idx]
	it, ok := s.items[key]
	return it, ok
}

func (s *Store) Upsert(key, val string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, exists := s.items[key]
	s.items[key] = Item{Key: key, Value: val, Modified: true}
	if !exists {
		s.order = insertSortedUnique(s.order, key)
	}
	s.applyFilterLocked(s.query)
	s.dirty = true
	_ = os.Setenv(key, val)
}

func (s *Store) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if it, ok := s.items[key]; ok {
		it.Deleted = true
		it.Modified = true
		s.items[key] = it
	}
	delete(s.items, key)
	removeKey(&s.order, key)
	removeKey(&s.filtered, key)
	s.dirty = true
	_ = os.Unsetenv(key)
}

func (s *Store) Filter(query string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyFilterLocked(query)
}

func (s *Store) applyFilterLocked(query string) {
	s.query = query
	if query == "" {
		s.filtered = append([]string{}, s.order...)
		return
	}
	q := strings.ToLower(query)
	out := make([]string, 0, len(s.order))
	for _, k := range s.order {
		v := s.items[k].Value
		if strings.Contains(strings.ToLower(k), q) || strings.Contains(strings.ToLower(v), q) {
			out = append(out, k)
		}
	}
	s.filtered = out
}

func (s *Store) Dirty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dirty
}

func (s *Store) Export(path string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if path == "" {
		path = ".env"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, k := range s.order {
		it, ok := s.items[k]
		if !ok {
			continue
		}
		line := fmt.Sprintf("%s=%s\n", safeKey(k), quoteIfNeeded(it.Value))
		if _, err := w.WriteString(line); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return nil
}

func (s *Store) Import(path string) (int, error) {
	if path == "" {
		return 0, errors.New("import path required")
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	added := 0
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := parseKV(line)
		if !ok || key == "" {
			continue
		}
		s.Upsert(key, val)
		added++
	}
	if err := sc.Err(); err != nil {
		return added, err
	}
	return added, nil
}

// Helpers

func insertSortedUnique(arr []string, key string) []string {
	i := sort.SearchStrings(arr, key)
	if i < len(arr) && arr[i] == key {
		return arr
	}
	arr = append(arr, "")
	copy(arr[i+1:], arr[i:])
	arr[i] = key
	return arr
}

func removeKey(arr *[]string, key string) {
	a := *arr
	for i, k := range a {
		if k == key {
			*arr = append(a[:i], a[i+1:]...)
			return
		}
	}
}

func safeKey(k string) string {
	return strings.TrimSpace(k)
}

func quoteIfNeeded(v string) string {
	if v == "" {
		return `""`
	}
	needs := strings.ContainsAny(v, " #\t\r\n\"'$")
	if !needs {
		return v
	}
	escaped := strings.ReplaceAll(v, `"`, `\"`)
	return `"` + escaped + `"`
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		inner := s[1 : len(s)-1]
		return strings.ReplaceAll(inner, `\"`, `"`)
	}
	return s
}

func parseKV(line string) (string, string, bool) {
	// Allow KEY=VALUE, ignoring export prefix
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "export ") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	}
	i := strings.IndexRune(line, '=')
	if i <= 0 {
		return "", "", false
	}
	key := strings.TrimSpace(line[:i])
	val := strings.TrimSpace(line[i+1:])
	return key, unquote(val), true
}
