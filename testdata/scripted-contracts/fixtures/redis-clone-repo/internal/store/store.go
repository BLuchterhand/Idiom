package store

import (
	"fmt"
	"strconv"
	"sync"
	"time"
)

type Clock func() time.Time

type Store struct {
	mu      sync.Mutex
	now     Clock
	strings map[string]string
	lists   map[string][]string
	expires map[string]time.Time
}

func New(clock Clock) *Store {
	if clock == nil {
		clock = time.Now
	}
	return &Store{
		now:     clock,
		strings: map[string]string{},
		lists:   map[string][]string{},
		expires: map[string]time.Time{},
	}
}

func (s *Store) Set(key, value string, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.strings[key] = value
	delete(s.lists, key)
	s.setTTL(key, ttl)
}

func (s *Store) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expiredLocked(key) {
		s.deleteLocked(key)
		return "", false
	}
	value, ok := s.strings[key]
	return value, ok
}

func (s *Store) Del(keys ...string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for _, key := range keys {
		if s.expiredLocked(key) {
			s.deleteLocked(key)
			continue
		}
		if _, ok := s.strings[key]; ok {
			removed++
		} else if _, ok := s.lists[key]; ok {
			removed++
		}
		s.deleteLocked(key)
	}
	return removed
}

func (s *Store) Incr(key string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expiredLocked(key) {
		s.deleteLocked(key)
	}
	current := int64(0)
	if raw, ok := s.strings[key]; ok {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("value is not an integer")
		}
		current = parsed
	}
	current++
	s.strings[key] = strconv.FormatInt(current, 10)
	delete(s.lists, key)
	return current, nil
}

func (s *Store) Expire(key string, ttl time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expiredLocked(key) {
		s.deleteLocked(key)
		return false
	}
	if _, ok := s.strings[key]; !ok {
		if _, ok := s.lists[key]; !ok {
			return false
		}
	}
	s.setTTL(key, ttl)
	return true
}

func (s *Store) TTL(key string) (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expiredLocked(key) {
		s.deleteLocked(key)
		return 0, false
	}
	if _, ok := s.strings[key]; !ok {
		if _, ok := s.lists[key]; !ok {
			return 0, false
		}
	}
	deadline, ok := s.expires[key]
	if !ok {
		return -1, true
	}
	remaining := time.Until(deadline)
	if s.now != nil {
		remaining = deadline.Sub(s.now())
	}
	if remaining < 0 {
		s.deleteLocked(key)
		return 0, false
	}
	return remaining, true
}

func (s *Store) LPush(key string, values ...string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expiredLocked(key) {
		s.deleteLocked(key)
	}
	if _, ok := s.strings[key]; ok {
		return 0, fmt.Errorf("key holds a string")
	}
	current := append([]string(nil), s.lists[key]...)
	for _, value := range values {
		current = append([]string{value}, current...)
	}
	s.lists[key] = current
	return len(current), nil
}

func (s *Store) LRange(key string, start, stop int) ([]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expiredLocked(key) {
		s.deleteLocked(key)
		return nil, false
	}
	values, ok := s.lists[key]
	if !ok {
		return nil, false
	}
	n := len(values)
	if n == 0 {
		return []string{}, true
	}
	if start < 0 {
		start = n + start
	}
	if stop < 0 {
		stop = n + stop
	}
	if start < 0 {
		start = 0
	}
	if stop >= n {
		stop = n - 1
	}
	if start >= n || start > stop {
		return []string{}, true
	}
	return append([]string(nil), values[start:stop+1]...), true
}

func (s *Store) setTTL(key string, ttl time.Duration) {
	if ttl > 0 {
		s.expires[key] = s.now().Add(ttl)
		return
	}
	delete(s.expires, key)
}

func (s *Store) expiredLocked(key string) bool {
	deadline, ok := s.expires[key]
	return ok && !s.now().Before(deadline)
}

func (s *Store) deleteLocked(key string) {
	delete(s.strings, key)
	delete(s.lists, key)
	delete(s.expires, key)
}