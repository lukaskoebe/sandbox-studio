package credentials

import (
	"context"
	"net/http"
	"time"
)

// An Issuer turns a long-lived secret into a short-lived credential. Issuers are code built
// into Studio, and few: the standards most services use. Recipes pick one by name.
type Issuer interface {
	Issue(ctx context.Context, in IssueInput) (Issued, error)
}

// IssueInput is what an issuer works from.
type IssueInput struct {
	Fields   map[string]string // the secret's fields, the user entered
	Secret   string            // the secret field's value
	Lifetime time.Duration     // as the recipe asks; the issuer may give less
	Session  string            // names the sandbox where the service records sessions
	Client   *http.Client
}

// Issued is a short-lived credential: its fields, for the recipe's answers, and when it
// expires.
type Issued struct {
	Fields  map[string]string
	Expires time.Time
}

var issuers = map[string]Issuer{
	"aws-sts": awsSTS{},
}
