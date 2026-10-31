package credentials

import (
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// A Recipe is a secret kind as data: which issuer makes its credentials, the fields the user
// enters, and how the sandbox's tools get them, through files, environment variables and the
// answers of `studio-agent credential`. Its templates run no code and never see the secret
// itself, only what was issued.
type Recipe struct {
	Name        string `yaml:"name"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Issuer      string `yaml:"issuer"`
	// Fields are what the user enters. The one secret field is the sealed value; the others
	// are kept as the secret's Fields.
	Fields []Field `yaml:"fields"`
	// Hosts are the hosts the credentials are for, as the secret's hosts default to.
	Hosts    []string `yaml:"hosts"`
	Lifetime duration `yaml:"lifetime"`
	// Env is set in a sandbox that sees exactly one secret of the kind; with more, the tool's
	// own setting picks one.
	Env map[string]string `yaml:"env"`
	// Files are managed blocks in the home, one entry of each secret of the kind.
	Files []File `yaml:"files"`
	// Answers are what `studio-agent credential <secret> <format>` prints, by format: an
	// object is printed as JSON, a string as it is.
	Answers map[string]any `yaml:"answers"`
}

// Field is a field of a secret kind's form.
type Field struct {
	Name     string `yaml:"name" json:"name"`
	Title    string `yaml:"title" json:"title"`
	Hint     string `yaml:"hint" json:"hint,omitempty"`
	Secret   bool   `yaml:"secret" json:"secret,omitempty"`
	Optional bool   `yaml:"optional" json:"optional,omitempty"`
	Pattern  string `yaml:"pattern" json:"pattern,omitempty"`
	pattern  *regexp.Regexp
}

// File is a managed block of a home file.
type File struct {
	Path    string `yaml:"path"`    // relative to the home directory
	Comment string `yaml:"comment"` // before each marker, for files without HTML comments
	Content string `yaml:"content"` // one secret's part of the block
}

type duration struct{ time.Duration }

func (d *duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	d.Duration = v
	return err
}

//go:embed recipes/*.yaml
var builtin embed.FS

// Recipes are the built-in kinds, by name.
var Recipes = mustLoad(builtin)

func mustLoad(fsys fs.FS) map[string]*Recipe {
	out, err := load(fsys)
	if err != nil {
		panic(err)
	}
	return out
}

func load(fsys fs.FS) (map[string]*Recipe, error) {
	names, err := fs.Glob(fsys, "recipes/*.yaml")
	if err != nil {
		return nil, err
	}
	out := map[string]*Recipe{}
	for _, name := range names {
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		var r Recipe
		dec := yaml.NewDecoder(strings.NewReader(string(raw)))
		dec.KnownFields(true)
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if err := r.check(); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out[r.Name] = &r
	}
	return out, nil
}

var (
	fieldName = regexp.MustCompile(`^[a-z][A-Za-z0-9]{0,31}$`)
	kindName  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

// check validates a recipe: one secret field, an issuer Studio has, and templates that name
// only what they may.
func (r *Recipe) check() error {
	if !kindName.MatchString(r.Name) || r.Title == "" {
		return fmt.Errorf("a recipe needs a name and a title")
	}
	if _, ok := issuers[r.Issuer]; !ok {
		return fmt.Errorf("unknown issuer %q", r.Issuer)
	}
	secrets := 0
	vars := map[string]bool{"secret": true}
	for i := range r.Fields {
		f := &r.Fields[i]
		if !fieldName.MatchString(f.Name) || f.Title == "" {
			return fmt.Errorf("field %q needs a name and a title", f.Name)
		}
		if f.Secret {
			secrets++
			if f.Optional {
				return fmt.Errorf("the secret field %s can't be optional", f.Name)
			}
			continue // a template never sees it
		}
		if f.Pattern != "" {
			p, err := regexp.Compile(f.Pattern)
			if err != nil {
				return fmt.Errorf("field %s: %w", f.Name, err)
			}
			f.pattern = p
		}
		vars["field."+f.Name] = true
	}
	if secrets != 1 {
		return fmt.Errorf("a recipe has one secret field, not %d", secrets)
	}
	if r.Lifetime.Duration < 15*time.Minute || r.Lifetime.Duration > 12*time.Hour {
		return fmt.Errorf("lifetime %s isn't between 15 minutes and 12 hours", r.Lifetime)
	}
	for name, v := range r.Env {
		if err := checkTemplate(v, vars); err != nil {
			return fmt.Errorf("env %s: %w", name, err)
		}
	}
	for _, f := range r.Files {
		if strings.HasPrefix(f.Path, "/") || strings.Contains(f.Path, "..") || len(f.Comment) > 8 {
			return fmt.Errorf("file %q: a path in the home, and a short comment", f.Path)
		}
		if err := checkTemplate(f.Content, vars); err != nil {
			return fmt.Errorf("file %s: %w", f.Path, err)
		}
		if strings.Contains(f.Content, agentproto.BlockBegin) || strings.Contains(f.Content, agentproto.BlockEnd) {
			return fmt.Errorf("file %s holds a block marker", f.Path)
		}
	}
	answerVars := map[string]bool{"cred.expires": true}
	for k := range vars {
		answerVars[k] = true
	}
	for format, a := range r.Answers {
		if err := walkStrings(a, func(s string) error { return checkTemplate(s, answerVars, "cred.") }); err != nil {
			return fmt.Errorf("answer %s: %w", format, err)
		}
	}
	return nil
}

// SecretField is the recipe's secret field.
func (r *Recipe) SecretField() Field {
	i := slices.IndexFunc(r.Fields, func(f Field) bool { return f.Secret })
	return r.Fields[i]
}

// CheckFields validates what the user entered for the fields other than the secret one, and
// returns them without the empty ones.
func (r *Recipe) CheckFields(in map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for k := range in {
		if !slices.ContainsFunc(r.Fields, func(f Field) bool { return f.Name == k && !f.Secret }) {
			return nil, fmt.Errorf("%s has no field %s", r.Title, k)
		}
	}
	for _, f := range r.Fields {
		if f.Secret {
			continue
		}
		v := strings.TrimSpace(in[f.Name])
		switch {
		case v == "" && !f.Optional:
			return nil, fmt.Errorf("%s needs %s", r.Title, f.Title)
		case v == "":
			continue
		case len(v) > 2048 || strings.ContainsAny(v, "\n\r\x00"):
			return nil, fmt.Errorf("%s isn't one line", f.Title)
		case f.pattern != nil && !f.pattern.MatchString(v):
			return nil, fmt.Errorf("%s doesn't look right", f.Title)
		}
		out[f.Name] = v
	}
	return out, nil
}

var templateVar = regexp.MustCompile(`\{\{\s*([A-Za-z][A-Za-z0-9.]*)\s*(?:\|\s*(lower|upper))?\s*\}\}`)

// checkTemplate checks that s names only vars, or names with one of prefixes.
func checkTemplate(s string, vars map[string]bool, prefixes ...string) error {
	rest := templateVar.ReplaceAllString(s, "")
	if strings.Contains(rest, "{{") || strings.Contains(rest, "}}") {
		return fmt.Errorf("a template it can't read: %q", s)
	}
	for _, m := range templateVar.FindAllStringSubmatch(s, -1) {
		if vars[m[1]] || slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(m[1], p) }) {
			continue
		}
		return fmt.Errorf("unknown name %s", m[1])
	}
	return nil
}

// render fills s's names from vars. A name vars lacks is empty: an optional field left out.
func render(s string, vars map[string]string) string {
	return templateVar.ReplaceAllStringFunc(s, func(m string) string {
		sub := templateVar.FindStringSubmatch(m)
		v := vars[sub[1]]
		switch sub[2] {
		case "lower":
			v = strings.ToLower(v)
		case "upper":
			v = strings.ToUpper(v)
		}
		return v
	})
}

// walkStrings calls f for each string in v, a value decoded from YAML.
func walkStrings(v any, f func(string) error) error {
	switch v := v.(type) {
	case string:
		return f(v)
	case map[string]any:
		for _, e := range v {
			if err := walkStrings(e, f); err != nil {
				return err
			}
		}
	case []any:
		for _, e := range v {
			if err := walkStrings(e, f); err != nil {
				return err
			}
		}
	}
	return nil
}

// renderValue renders the strings of v, a value decoded from YAML.
func renderValue(v any, vars map[string]string) any {
	switch v := v.(type) {
	case string:
		return render(v, vars)
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[k] = renderValue(e, vars)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = renderValue(e, vars)
		}
		return out
	}
	return v
}
