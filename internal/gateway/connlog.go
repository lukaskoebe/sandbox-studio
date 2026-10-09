package gateway

import (
	"sync"
	"time"
)

// Verdicts in the connection log.
const (
	VerdictPending   = "pending"   // waiting for a rule or the user
	VerdictOpen      = "open"      // allowed and still connected
	VerdictAllowed   = "allowed"   // allowed, now closed
	VerdictDenied    = "denied"    // a rule or the user refused it
	VerdictUndecided = "undecided" // nobody answered in time
	VerdictFailed    = "failed"    // an error, e.g. the upstream didn't answer
)

// Conn is one connection in a sandbox's connection log.
type Conn struct {
	ID       uint64     `json:"id"`
	Started  time.Time  `json:"started"`
	Ended    *time.Time `json:"ended,omitempty"`
	Host     string     `json:"host"`
	Port     int        `json:"port"`
	Address  string     `json:"address" doc:"The destination as the sandbox asked for it"`
	Source   string     `json:"source" enum:"tls,http,dns,socks,ip" doc:"Where the host name came from"`
	Verdict  string     `json:"verdict" enum:"pending,open,allowed,denied,undecided,failed"`
	RuleID   string     `json:"ruleId,omitempty"`
	Error    string     `json:"error,omitempty"`
	Sent     int64      `json:"sent" doc:"Bytes sent by the sandbox"`
	Received int64      `json:"received" doc:"Bytes received by the sandbox"`
}

// connLogSize is how many connections are kept per sandbox. The log lives in memory only.
const connLogSize = 500

// ConnLog keeps the most recent connections of each sandbox. The zero value is ready.
type ConnLog struct {
	mu        sync.Mutex
	next      uint64
	sandboxes map[string]*[]*Conn
}

func (l *ConnLog) add(sandboxID string, c Conn) *Conn {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sandboxes == nil {
		l.sandboxes = map[string]*[]*Conn{}
	}
	l.next++
	c.ID, c.Started, c.Verdict = l.next, time.Now(), VerdictPending
	entries, ok := l.sandboxes[sandboxID]
	if !ok {
		entries = &[]*Conn{}
		l.sandboxes[sandboxID] = entries
	}
	if len(*entries) >= connLogSize {
		*entries = append((*entries)[:0], (*entries)[1:]...)
	}
	*entries = append(*entries, &c)
	return &c
}

func (l *ConnLog) opened(c *Conn, ruleID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c.Verdict, c.RuleID = VerdictOpen, ruleID
}

func (l *ConnLog) finish(c *Conn, verdict, ruleID, problem string, sent, received int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	c.Ended, c.Verdict, c.RuleID, c.Error, c.Sent, c.Received = &now, verdict, ruleID, problem, sent, received
}

// List returns a sandbox's logged connections, newest first.
func (l *ConnLog) List(sandboxID string) []Conn {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []Conn{}
	if entries, ok := l.sandboxes[sandboxID]; ok {
		for i := len(*entries) - 1; i >= 0; i-- {
			out = append(out, *(*entries)[i])
		}
	}
	return out
}

// Forget drops the log of a deleted sandbox.
func (l *ConnLog) Forget(sandboxID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.sandboxes, sandboxID)
}
