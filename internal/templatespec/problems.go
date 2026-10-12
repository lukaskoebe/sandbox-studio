package templatespec

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// maxProblems bounds how many problems one parse reports.
const maxProblems = 50

// yamlErrorLine matches the position prefix of go-yaml syntax errors, which
// carry a line but no column.
var yamlErrorLine = regexp.MustCompile(`^yaml: line (\d+): (.*)$`)

// Problem is one reason a YAML spec is invalid. Line and Column are 1-based
// positions in the source (Column counts characters, not bytes). Column is 0
// when only the line is known; both are 0 when the problem concerns the whole
// input.
type Problem struct {
	Line    int
	Column  int
	Message string
}

func (p Problem) String() string {
	switch {
	case p.Line > 0 && p.Column > 0:
		return fmt.Sprintf("line %d, column %d: %s", p.Line, p.Column, p.Message)
	case p.Line > 0:
		return fmt.Sprintf("line %d: %s", p.Line, p.Message)
	default:
		return p.Message
	}
}

// Error lists the problems ParseYAML found, in source order where possible.
// errors.Is(err, ErrInvalid) holds for it.
type Error struct {
	Problems []Problem
}

func (e *Error) Error() string {
	parts := make([]string, len(e.Problems))
	for i, problem := range e.Problems {
		parts[i] = problem.String()
	}
	return ErrInvalid.Error() + ": " + strings.Join(parts, "; ")
}

// Is makes every *Error match ErrInvalid.
func (e *Error) Is(target error) bool { return target == ErrInvalid }

// Problems returns the problems behind a spec error. A plain ErrInvalid error
// becomes one problem without a position; other errors return nil.
func Problems(err error) []Problem {
	var specErr *Error
	if errors.As(err, &specErr) {
		return specErr.Problems
	}
	if errors.Is(err, ErrInvalid) {
		return []Problem{{Message: problemMessage(err)}}
	}
	return nil
}

// problems collects positioned problems while a spec is parsed.
type problems struct {
	list []Problem
}

func (p *problems) add(node *yaml.Node, format string, args ...any) {
	if len(p.list) >= maxProblems {
		return
	}
	problem := Problem{Message: fmt.Sprintf(format, args...)}
	if node != nil {
		problem.Line, problem.Column = node.Line, node.Column
	}
	// One problem per position: a key can fail both as a key and as a node.
	for _, existing := range p.list {
		if problem.Line > 0 && existing.Line == problem.Line && existing.Column == problem.Column {
			return
		}
	}
	p.list = append(p.list, problem)
}

// addErr records an ErrInvalid error from a node-level check at node.
func (p *problems) addErr(node *yaml.Node, err error) {
	p.add(node, "%s", problemMessage(err))
}

func (p *problems) err() error {
	if len(p.list) == 0 {
		return nil
	}
	// The tree walk checks a mapping's keys before its values.
	sort.SliceStable(p.list, func(i, j int) bool {
		a, b := p.list[i], p.list[j]
		return a.Line < b.Line || (a.Line == b.Line && a.Column < b.Column)
	})
	return &Error{Problems: p.list}
}

// inputError reports a problem that concerns the whole input.
func inputError(err error) error {
	return &Error{Problems: []Problem{{Message: problemMessage(err)}}}
}

// syntaxError turns a go-yaml decode error into a problem on its line.
func syntaxError(prefix string, err error) error {
	message := err.Error()
	problem := Problem{Message: prefix + strings.TrimPrefix(message, "yaml: ")}
	if match := yamlErrorLine.FindStringSubmatch(message); match != nil {
		if line, convErr := strconv.Atoi(match[1]); convErr == nil {
			problem = Problem{Line: line, Message: prefix + match[2]}
		}
	}
	return &Error{Problems: []Problem{problem}}
}

func problemMessage(err error) string {
	return strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": ")
}

// at picks where to report a problem with a mapping value: the value itself
// when it starts on the key's line, otherwise the key, so a problem with a
// block collection is not reported on its first child. An empty value is
// reported at its key.
func at(key, value *yaml.Node) *yaml.Node {
	empty := value.Kind == yaml.ScalarNode && value.Tag == "!!null" && value.Value == ""
	if value.Line == key.Line && !empty {
		return value
	}
	return key
}
