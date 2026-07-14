package controlplane

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// buildScheduler is the independent build-slot pool (SIMURGH_BUILD_SLOTS).
type buildScheduler struct {
	slots int
	clock func() time.Time

	mu     sync.Mutex
	active map[string]*BuildGrant // grantID → grant
	byJob  map[string]string      // jobID → grantID
	queue  []buildWaiter
	idSeq  atomic.Uint64
}

type buildWaiter struct {
	jobID    string
	enqueued time.Time
	ch       chan struct{}
	canceled bool
}

func newBuildScheduler(slots int, clock func() time.Time) *buildScheduler {
	return &buildScheduler{
		slots:  slots,
		clock:  clock,
		active: make(map[string]*BuildGrant),
		byJob:  make(map[string]string),
	}
}

func (s *buildScheduler) status() BuildStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	queued := 0
	for _, w := range s.queue {
		if !w.canceled {
			queued++
		}
	}
	return BuildStatus{
		BuildSlots:   s.slots,
		ActiveBuilds: len(s.active),
		QueuedBuilds: queued,
	}
}

func (s *buildScheduler) lookup(grantID string) (*BuildGrant, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.active[grantID]
	if !ok {
		return nil, false
	}
	cp := *g
	return &cp, true
}

// acquire grants a build slot or enqueues the job. When wait is false and the
// pool is full the job is still enqueued (for queuedBuilds metadata) and a
// BuildQueuedError is returned — analogous to broker CAPACITY_QUEUED leases.
func (s *buildScheduler) acquire(ctx context.Context, jobID string, wait bool) (grant BuildGrant, queuedAt, dequeuedAt time.Time, queueWait time.Duration, err error) {
	var waitedFrom time.Time
	for {
		s.mu.Lock()
		if existing, ok := s.byJob[jobID]; ok {
			g := *s.active[existing]
			s.mu.Unlock()
			if !waitedFrom.IsZero() {
				dequeuedAt = s.clock()
				return g, waitedFrom, dequeuedAt, dequeuedAt.Sub(waitedFrom), nil
			}
			return g, time.Time{}, time.Time{}, 0, nil
		}
		pos := s.queuePositionLocked(jobID)
		canTake := len(s.active) < s.slots && (pos == 1 || (pos == 0 && !s.hasLiveWaitersAheadLocked(jobID)))
		if canTake {
			g := s.grantLocked(jobID)
			s.mu.Unlock()
			if !waitedFrom.IsZero() {
				dequeuedAt = g.GrantedAt
				return g, waitedFrom, dequeuedAt, dequeuedAt.Sub(waitedFrom), nil
			}
			return g, time.Time{}, time.Time{}, 0, nil
		}

		// Ensure queued for metadata / waiter.
		var ch chan struct{}
		if pos == 0 {
			w := buildWaiter{
				jobID:    jobID,
				enqueued: s.clock(),
				ch:       make(chan struct{}, 1),
			}
			if waitedFrom.IsZero() {
				waitedFrom = w.enqueued
			}
			s.queue = append(s.queue, w)
			ch = w.ch
			pos = s.queuePositionLocked(jobID)
		} else {
			for i := range s.queue {
				if s.queue[i].jobID == jobID && !s.queue[i].canceled {
					ch = s.queue[i].ch
					if waitedFrom.IsZero() {
						waitedFrom = s.queue[i].enqueued
					}
					break
				}
			}
		}

		if !wait {
			active := len(s.active)
			slots := s.slots
			s.mu.Unlock()
			return BuildGrant{}, waitedFrom, time.Time{}, 0, buildQueued(pos, slots, active)
		}

		s.mu.Unlock()
		if ch == nil {
			continue
		}
		select {
		case <-ctx.Done():
			s.cancelWaiter(jobID)
			return BuildGrant{}, waitedFrom, time.Time{}, 0, ctx.Err()
		case <-ch:
			// Retry grant after promotion signal.
			continue
		}
	}
}

func (s *buildScheduler) hasLiveWaitersAheadLocked(jobID string) bool {
	for _, w := range s.queue {
		if w.canceled {
			continue
		}
		if w.jobID == jobID {
			return false
		}
		return true
	}
	return false
}

func (s *buildScheduler) grantLocked(jobID string) BuildGrant {
	id := fmt.Sprintf("build-%d", s.idSeq.Add(1))
	g := &BuildGrant{ID: id, JobID: jobID, GrantedAt: s.clock()}
	s.active[id] = g
	s.byJob[jobID] = id
	s.removeWaiterLocked(jobID)
	return *g
}

func (s *buildScheduler) release(jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	grantID, ok := s.byJob[jobID]
	if ok {
		delete(s.active, grantID)
		delete(s.byJob, jobID)
	}
	s.removeWaiterLocked(jobID)
	s.promoteLocked()
	return nil
}

func (s *buildScheduler) promoteLocked() {
	for len(s.active) < s.slots {
		idx := -1
		for i, w := range s.queue {
			if !w.canceled {
				idx = i
				break
			}
		}
		if idx < 0 {
			return
		}
		w := s.queue[idx]
		// Auto-grant the head of the queue (including Wait=false metadata
		// entries) so a freed slot is not skipped while FIFO waiters remain.
		s.grantLocked(w.jobID)
		select {
		case w.ch <- struct{}{}:
		default:
		}
	}
}

func (s *buildScheduler) cancelWaiter(jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeWaiterLocked(jobID)
}

func (s *buildScheduler) removeWaiterLocked(jobID string) {
	out := s.queue[:0]
	for _, w := range s.queue {
		if w.jobID == jobID {
			continue
		}
		out = append(out, w)
	}
	s.queue = out
}

func (s *buildScheduler) queuePositionLocked(jobID string) int {
	pos := 0
	for _, w := range s.queue {
		if w.canceled {
			continue
		}
		pos++
		if w.jobID == jobID {
			return pos
		}
	}
	return 0
}
