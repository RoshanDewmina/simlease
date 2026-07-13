package broker

import "fmt"

// SpecKey returns the FIFO-queue key for a DeviceSpec (platform|model|runtime).
func SpecKey(spec DeviceSpec) string {
	return fmt.Sprintf("%s|%s|%s", spec.Platform, spec.Model, spec.Runtime)
}

// fifoQueue is a per-spec-key FIFO of lease IDs.
type fifoQueue struct {
	ids []string
}

func (q *fifoQueue) enqueue(id string) {
	q.ids = append(q.ids, id)
}

func (q *fifoQueue) dequeue() (string, bool) {
	if len(q.ids) == 0 {
		return "", false
	}
	id := q.ids[0]
	q.ids = q.ids[1:]
	return id, true
}

func (q *fifoQueue) remove(id string) bool {
	for i, x := range q.ids {
		if x == id {
			q.ids = append(q.ids[:i], q.ids[i+1:]...)
			return true
		}
	}
	return false
}

// position is 1-based; 0 means not in queue.
func (q *fifoQueue) position(id string) int {
	for i, x := range q.ids {
		if x == id {
			return i + 1
		}
	}
	return 0
}
