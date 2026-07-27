package pipeline

import "sync"

type job struct {
	Word string
	Slug string
}

// jobQueue 是带高优先级插队的阻塞队列。regenerate 走 front=true，
// 让人工纠错请求越过批量导入的积压。
type jobQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	high   []job
	normal []job
	closed bool
}

func newJobQueue() *jobQueue {
	q := &jobQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *jobQueue) push(j job, front bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	if front {
		q.high = append(q.high, j)
	} else {
		q.normal = append(q.normal, j)
	}
	q.cond.Signal()
}

// pop 阻塞到有任务或队列关闭；关闭后返回 false。
func (q *jobQueue) pop() (job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.high) == 0 && len(q.normal) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.high) > 0 {
		j := q.high[0]
		q.high = q.high[1:]
		return j, true
	}
	if len(q.normal) > 0 {
		j := q.normal[0]
		q.normal = q.normal[1:]
		return j, true
	}
	return job{}, false
}

func (q *jobQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.cond.Broadcast()
}
