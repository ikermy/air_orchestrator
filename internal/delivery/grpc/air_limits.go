package grpc

import (
	"os"
	"strconv"
	"sync"
)

// deviceSemaphore ограничивает число одновременных стримов на устройство.
type deviceSemaphore struct {
	limit  int
	mu     sync.Mutex
	counts map[string]int
}

func newDeviceSemaphore(limit int) *deviceSemaphore {
	if limit <= 0 {
		limit = 1
	}
	return &deviceSemaphore{limit: limit, counts: make(map[string]int)}
}

func (s *deviceSemaphore) acquire(deviceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.counts[deviceID] >= s.limit {
		return false
	}
	s.counts[deviceID]++
	return true
}

func (s *deviceSemaphore) release(deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.counts[deviceID] <= 1 {
		delete(s.counts, deviceID)
		return
	}
	s.counts[deviceID]--
}

func envLimit(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
