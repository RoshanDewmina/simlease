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
	// promoted retains ownership between FIFO removal and acquire's ready
	// branch. Context cancellation can therefore release the exact grant.
	promoted map[string]buildWaiter // jobID → promoted waiter
	idSeq    atomic.Uint64
}

type buildWaiter struct {
	jobID    string
	enqueued time.Time
	ch       chan BuildGrant
	grantID  string
}

func newBuildScheduler(slots int, clock func() time.Time) *buildScheduler {
	return &buildScheduler{
		slots:    slots,
		clock:    clock,
		active:   make(map[string]*BuildGrant),
		byJob:    make(map[string]string),
		promoted: make(map[string]buildWaiter),
	}
}

func (s *buildScheduler) status() BuildStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return BuildStatus{
		BuildSlots:   s.slots,
		ActiveBuilds: len(s.active),
		QueuedBuilds: len(s.queue),
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

// acquire grants a build slot or, only when wait is true, enqueues the job.
// Wait=false returns retry metadata without leaving a durable waiter.
func (s *buildScheduler) acquire(ctx context.Context, jobID string, wait bool) (grant BuildGrant, queuedAt, dequeuedAt time.Time, queueWait time.Duration, err error) {
	s.mu.Lock()
	if existing, ok := s.byJob[jobID]; ok {
		g := *s.active[existing]
		s.mu.Unlock()
		return g, time.Time{}, time.Time{}, 0, nil
	}
	if len(s.active) < s.slots && len(s.queue) == 0 {
		g := s.grantLocked(jobID)
		s.mu.Unlock()
		return g, time.Time{}, time.Time{}, 0, nil
	}
	position := len(s.queue) + 1
	if !wait {
		active, slots := len(s.active), s.slots
		s.mu.Unlock()
		return BuildGrant{}, time.Time{}, time.Time{}, 0, buildQueued(position, slots, active)
	}
	for _, w := range s.queue {
		if w.jobID == jobID {
			s.mu.Unlock()
			return BuildGrant{}, time.Time{}, time.Time{}, 0, fmt.Errorf("controlplane: job %q already waiting for build slot", jobID)
		}
	}
	waiter := buildWaiter{
		jobID:    jobID,
		enqueued: s.clock(),
		ch:       make(chan BuildGrant, 1),
	}
	s.queue = append(s.queue, waiter)
	s.mu.Unlock()

	select {
	case <-ctx.Done():
		s.cancelWaiter(jobID)
		return BuildGrant{}, waiter.enqueued, time.Time{}, 0, ctx.Err()
	case g := <-waiter.ch:
		if g.ID == "" {
			return BuildGrant{}, waiter.enqueued, time.Time{}, 0, context.Canceled
		}
		if !s.takePromoted(jobID, g.ID) {
			return BuildGrant{}, waiter.enqueued, time.Time{}, 0, context.Canceled
		}
		return g, waiter.enqueued, g.GrantedAt, g.GrantedAt.Sub(waiter.enqueued), nil
	}
}

func (s *buildScheduler) takePromoted(jobID, grantID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.promoted[jobID]
	if !ok || w.grantID != grantID {
		return false
	}
	delete(s.promoted, jobID)
	return true
}

func (s *buildScheduler) grantLocked(jobID string) BuildGrant {
	id := fmt.Sprintf("build-%d", s.idSeq.Add(1))
	g := &BuildGrant{ID: id, JobID: jobID, GrantedAt: s.clock()}
	s.active[id] = g
	s.byJob[jobID] = id
	return *g
}

func (s *buildScheduler) release(jobID string) error {
	return s.releaseOwned(jobID, "")
}

// releaseOwned releases only the named grant. Empty grantID keeps the
// compatibility behavior for explicit ReleaseBuildSlot calls.
func (s *buildScheduler) releaseOwned(jobID, grantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.byJob[jobID]
	if ok {
		if grantID != "" && current != grantID {
			return nil
		}
		delete(s.active, current)
		delete(s.byJob, jobID)
		delete(s.promoted, jobID)
	}
	if grantID == "" {
		s.removeWaiterLocked(jobID, true)
	}
	s.promoteLocked()
	return nil
}

func (s *buildScheduler) promoteLocked() {
	for len(s.active) < s.slots {
		if len(s.queue) == 0 {
			return
		}
		w := s.queue[0]
		s.queue = s.queue[1:]
		g := s.grantLocked(w.jobID)
		w.grantID = g.ID
		s.promoted[w.jobID] = w
		w.ch <- g
	}
}

func (s *buildScheduler) cancelWaiter(jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.removeWaiterLocked(jobID, true) {
		s.promoteLocked()
		return
	}
	w, ok := s.promoted[jobID]
	if !ok {
		return
	}
	delete(s.promoted, jobID)
	if current, active := s.byJob[jobID]; active && current == w.grantID {
		delete(s.active, current)
		delete(s.byJob, jobID)
	}
	s.promoteLocked()
}

func (s *buildScheduler) removeWaiterLocked(jobID string, notify bool) bool {
	out := s.queue[:0]
	removed := false
	for _, w := range s.queue {
		if w.jobID == jobID {
			removed = true
			if notify {
				w.ch <- BuildGrant{}
			}
			continue
		}
		out = append(out, w)
	}
	s.queue = out
	return removed
}
