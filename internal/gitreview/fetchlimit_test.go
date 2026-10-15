package gitreview

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// fakeClock is a settable clock for fetchLimiter.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newLimiter() (*fetchLimiter, *fakeClock) {
	c := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	return &fetchLimiter{now: c.now}, c
}

func TestMirrorFetchIsSkippedWithinTheInterval(t *testing.T) {
	l, clock := newLimiter()
	ctx := context.Background()
	var n int
	fn := func(context.Context) error { n++; return nil }
	for range 3 {
		if err := l.fetch(ctx, "/a", "env/forge/o/r", fn); err != nil {
			t.Fatal(err)
		}
	}
	if n != 1 {
		t.Fatalf("fetches within the interval = %d, want 1", n)
	}
	// Another mirror isn't affected.
	if err := l.fetch(ctx, "/b", "env/forge/o/r", fn); err != nil || n != 2 {
		t.Fatalf("other mirror: n = %d, err = %v", n, err)
	}
	clock.add(MirrorFetchInterval - time.Second)
	if err := l.fetch(ctx, "/a", "env/forge/o/r", fn); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("fetched again before the interval ended (n = %d)", n)
	}
	clock.add(time.Second)
	if err := l.fetch(ctx, "/a", "env/forge/o/r", fn); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("did not fetch again after the interval (n = %d)", n)
	}
}

func TestFailedMirrorFetchIsNotRemembered(t *testing.T) {
	l, _ := newLimiter()
	ctx := context.Background()
	boom := errors.New("agent refused to sign")
	var n int
	fail := func(context.Context) error { n++; return boom }
	if err := l.fetch(ctx, "/a", "r", fail); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if err := l.fetch(ctx, "/a", "r", fail); !errors.Is(err, boom) || n != 2 {
		t.Fatalf("second fetch after a failure: n = %d, err = %v", n, err)
	}
}

func TestConcurrentMirrorFetchesShareOne(t *testing.T) {
	l, _ := newLimiter()
	ctx := context.Background()
	release := make(chan struct{})
	started := make(chan struct{}, 10)
	var n atomic.Int32
	boom := errors.New("forge unreachable")
	fn := func(context.Context) error {
		n.Add(1)
		started <- struct{}{}
		<-release
		return boom
	}
	const callers = 5
	errs := make(chan error, callers)
	go func() { errs <- l.fetch(ctx, "/a", "r", fn) }()
	<-started
	for range callers - 1 {
		go func() { errs <- l.fetch(ctx, "/a", "r", fn) }()
	}
	waitFor(t, func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.mirrors["/a"].flight.callers == callers
	})
	close(release)
	for range callers {
		if err := <-errs; !errors.Is(err, boom) {
			t.Fatalf("a caller got %v, want the shared fetch's error", err)
		}
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("fetches = %d, want 1", got)
	}
}

func TestMirrorFetchOutlivesACallerHangingUp(t *testing.T) {
	l, _ := newLimiter()
	release := make(chan struct{})
	var n atomic.Int32
	var fetchCtxErr atomic.Value
	fn := func(ctx context.Context) error {
		n.Add(1)
		<-release
		if err := ctx.Err(); err != nil {
			fetchCtxErr.Store(err)
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- l.fetch(ctx, "/a", "r", fn) }()
	waitFor(t, func() bool { return n.Load() == 1 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller hanging up got %v", err)
	}
	// A second caller joins the fetch still running and gets its result.
	second := make(chan error, 1)
	go func() { second <- l.fetch(context.Background(), "/a", "r", fn) }()
	waitFor(t, func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.mirrors["/a"].flight.callers == 2
	})
	close(release)
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if n.Load() != 1 || fetchCtxErr.Load() != nil {
		t.Fatalf("fetches = %d, fetch context error = %v", n.Load(), fetchCtxErr.Load())
	}
	// And it counts for the interval.
	if err := l.fetch(context.Background(), "/a", "r", fn); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 1 {
		t.Fatal("a completed fetch whose first caller hung up did not count")
	}
}

func TestForgetMakesEveryMirrorOfTheRepositoryFetch(t *testing.T) {
	l, _ := newLimiter()
	ctx := context.Background()
	count := map[string]int{}
	fn := func(path string) func(context.Context) error {
		return func(context.Context) error { count[path]++; return nil }
	}
	if err := l.fetch(ctx, "/sb1/r", "env/f/o/r", fn("/sb1/r")); err != nil {
		t.Fatal(err)
	}
	if err := l.fetch(ctx, "/sb2/r", "env/f/o/r", fn("/sb2/r")); err != nil {
		t.Fatal(err)
	}
	if err := l.fetch(ctx, "/sb1/other", "env/f/o/other", fn("/sb1/other")); err != nil {
		t.Fatal(err)
	}
	l.forget("env/f/o/r")
	if err := l.fetch(ctx, "/sb1/r", "env/f/o/r", fn("/sb1/r")); err != nil {
		t.Fatal(err)
	}
	if err := l.fetch(ctx, "/sb2/r", "env/f/o/r", fn("/sb2/r")); err != nil {
		t.Fatal(err)
	}
	if err := l.fetch(ctx, "/sb1/other", "env/f/o/other", fn("/sb1/other")); err != nil {
		t.Fatal(err)
	}
	if count["/sb1/r"] != 2 || count["/sb2/r"] != 2 || count["/sb1/other"] != 1 {
		t.Fatalf("fetches = %v", count)
	}

	// A fetch running while the repository is pushed to doesn't count as fresh.
	release := make(chan struct{})
	var n atomic.Int32
	slow := func(context.Context) error { n.Add(1); <-release; return nil }
	l2, _ := newLimiter()
	done := make(chan error, 1)
	go func() { done <- l2.fetch(ctx, "/a", "r", slow) }()
	waitFor(t, func() bool { return n.Load() == 1 })
	l2.forget("r")
	close(release)
	<-done
	if err := l2.fetch(ctx, "/a", "r", func(context.Context) error { n.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 {
		t.Fatalf("fetches = %d, want 2: the one running during forget counted", n.Load())
	}
}

func TestLimiterDropsStaleMirrors(t *testing.T) {
	l, clock := newLimiter()
	ctx := context.Background()
	ok := func(context.Context) error { return nil }
	if err := l.fetch(ctx, "/a", "r", ok); err != nil {
		t.Fatal(err)
	}
	clock.add(MirrorFetchInterval)
	if err := l.fetch(ctx, "/b", "r", ok); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.mirrors["/a"]; ok || len(l.mirrors) != 1 {
		t.Fatalf("mirrors = %v", l.mirrors)
	}
}

// TestPushesWithinTheIntervalFetchTheMirrorOnce runs through the virtual remote: a second
// push right after the first advertises the staging mirror as it is, and an approved push
// makes the next one fetch again.
func TestPushesWithinTheIntervalFetchTheMirrorOnce(t *testing.T) {
	h := newHarness(t)
	g := h.guest(t, h.env.ID, "one")
	repo := g.clone(t)
	uploads := func() int {
		h.forge.mu.Lock()
		defer h.forge.mu.Unlock()
		return h.forge.upload
	}
	before := uploads()
	commitFile(t, repo, "a.txt", "a\n", "A")
	if err := push(repo, "refs/heads/main:refs/heads/a", false); err != nil {
		t.Fatal(err)
	}
	if got := uploads() - before; got != 1 {
		t.Fatalf("first push fetched the mirror %d times, want 1", got)
	}
	commitFile(t, repo, "b.txt", "b\n", "B")
	if err := push(repo, "refs/heads/main:refs/heads/b", false); err != nil {
		t.Fatal(err)
	}
	if got := uploads() - before; got != 1 {
		t.Fatalf("second push within the interval fetched again (%d fetches)", got)
	}
	pending := h.approvals(t, KindPush, store.StatusPending)
	if len(pending) != 2 {
		t.Fatalf("pending = %d", len(pending))
	}
	h.decide(t, pending[0], integrations.Allow, "")
	commitFile(t, repo, "c.txt", "c\n", "C")
	if err := push(repo, "refs/heads/main:refs/heads/c", false); err != nil {
		t.Fatal(err)
	}
	if got := uploads() - before; got != 2 {
		t.Fatalf("push after an approved push fetched %d times in all, want 2", got)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}
