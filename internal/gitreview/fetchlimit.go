package gitreview

import (
	"context"
	"sync"
	"time"
)

// MirrorFetchInterval is how long a staging mirror counts as fresh after a fetch from the
// forge. Fetches over the host's SSH agent may ask the user to confirm a signature (a
// hardware key, a password manager), so a burst of pushes from one sandbox must not
// prompt once per push.
const MirrorFetchInterval = 30 * time.Second

// mirrorFetchTimeout bounds one mirror fetch, which may wait for such a confirmation.
const mirrorFetchTimeout = 2 * time.Minute

// fetchLimiter rate-limits upstream fetches per staging mirror: callers asking for a
// fetch while one is running share its result, and a mirror that fetched successfully
// within the interval is used as it is. A failed fetch is not remembered, so the next
// caller tries again.
type fetchLimiter struct {
	interval time.Duration    // MirrorFetchInterval if zero
	now      func() time.Time // time.Now if nil

	mu      sync.Mutex
	mirrors map[string]*mirrorFetch // staging path -> state
}

type mirrorFetch struct {
	repo    string    // environment, forge and path: every sandbox's mirror of one repository
	fetched time.Time // when the last fetch that counts finished; zero if none
	gen     int       // bumped by forget, so a fetch already running doesn't count
	flight  *fetchFlight
}

type fetchFlight struct {
	done    chan struct{}
	err     error
	callers int // callers that started or joined it
}

func (l *fetchLimiter) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

func (l *fetchLimiter) every() time.Duration {
	if l.interval > 0 {
		return l.interval
	}
	return MirrorFetchInterval
}

// fetch runs fn for the mirror at path unless it is fresh, or waits for the fetch of it
// that is already running. fn runs with a context that doesn't end with ctx, bounded by
// mirrorFetchTimeout, so a caller hanging up doesn't fail the others waiting on it.
func (l *fetchLimiter) fetch(ctx context.Context, path, repo string, fn func(context.Context) error) error {
	l.mu.Lock()
	if l.mirrors == nil {
		l.mirrors = map[string]*mirrorFetch{}
	}
	m := l.mirrors[path]
	if m == nil {
		m = &mirrorFetch{repo: repo}
		l.mirrors[path] = m
	}
	f := m.flight
	if f == nil {
		if !m.fetched.IsZero() && l.clock().Sub(m.fetched) < l.every() {
			l.mu.Unlock()
			return nil
		}
		f = &fetchFlight{done: make(chan struct{})}
		m.flight = f
		go l.run(context.WithoutCancel(ctx), m, f, m.gen, fn)
	}
	f.callers++
	l.mu.Unlock()
	select {
	case <-f.done:
		return f.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *fetchLimiter) run(ctx context.Context, m *mirrorFetch, f *fetchFlight, gen int, fn func(context.Context) error) {
	ctx, cancel := context.WithTimeout(ctx, mirrorFetchTimeout)
	err := fn(ctx)
	cancel()
	l.mu.Lock()
	defer l.mu.Unlock()
	f.err = err
	m.flight = nil
	if err == nil && m.gen == gen {
		m.fetched = l.clock()
	}
	// Drop what no longer saves a fetch, so the map holds only fresh or running mirrors.
	now := l.clock()
	for p, o := range l.mirrors {
		if o.flight == nil && now.Sub(o.fetched) >= l.every() {
			delete(l.mirrors, p)
		}
	}
	close(f.done)
}

// forget makes every sandbox's mirror of repo fetch again on its next use, including
// one whose fetch is running now. Studio calls it after pushing to the repository, which
// moves the forge's branches past what the mirrors hold.
func (l *fetchLimiter) forget(repo string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, m := range l.mirrors {
		if m.repo == repo {
			m.fetched = time.Time{}
			m.gen++
		}
	}
}
