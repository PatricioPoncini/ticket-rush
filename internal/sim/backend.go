package sim

import "time"

// backend is the checkout service: a fixed pool of workers in front of an
// unbounded FIFO queue. It is deliberately naive: it does not know whether
// the client is still waiting, so once the queue wait exceeds the client
// timeout it keeps doing work whose answer nobody will read. That is the
// mechanism behind the throughput vs goodput gap described in the AWS
// Builders' Library article "Using load shedding to avoid overload".
type backend struct {
	e       *engine
	workers int
	busy    int
	service time.Duration
	queue   []*request
	head    int
}

func newBackend(e *engine, workers int, service time.Duration) *backend {
	return &backend{e: e, workers: workers, service: service}
}

func (b *backend) queued() int { return len(b.queue) - b.head }

// estimatedWait is how long a request arriving now would sit in the queue.
func (b *backend) estimatedWait() time.Duration {
	return time.Duration(b.queued()) * b.service / time.Duration(b.workers)
}

func (b *backend) enqueue(r *request) {
	if b.busy < b.workers {
		b.start(r)
		return
	}
	b.queue = append(b.queue, r)
}

func (b *backend) start(r *request) {
	b.busy++
	b.e.after(b.service, func() { b.finish(r) })
}

func (b *backend) finish(r *request) {
	b.busy--
	b.e.completed(r)
	if b.head < len(b.queue) {
		next := b.queue[b.head]
		b.queue[b.head] = nil
		b.head++
		if b.head > 4096 && b.head*2 > len(b.queue) {
			b.queue = append([]*request(nil), b.queue[b.head:]...)
			b.head = 0
		}
		b.start(next)
	}
}
