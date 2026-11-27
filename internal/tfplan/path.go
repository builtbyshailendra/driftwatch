package tfplan

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// IsIdentifier reports whether a map key renders in dotted form (tags.Name)
// rather than in brackets (tags["kubernetes.io/role"]).
func IsIdentifier(key string) bool { return identifier.MatchString(key) }

// Step is one element of an attribute path: a map or object key, or a list
// index.
type Step struct {
	Key     string
	Index   int
	IsIndex bool
}

// Path is an attribute path inside a resource, for example tags["Name"].
type Path []Step

// KeyStep returns a step addressing a map key or object attribute.
func KeyStep(k string) Step { return Step{Key: k} }

// IndexStep returns a step addressing a list or set element.
func IndexStep(i int) Step { return Step{Index: i, IsIndex: true} }

// Append returns a copy of p with s appended.
func (p Path) Append(s Step) Path {
	out := make(Path, len(p), len(p)+1)
	copy(out, p)
	return append(out, s)
}

// String renders the path in Terraform-like syntax: identifier keys are joined
// with dots, other keys are quoted in brackets and list indexes are bracketed.
func (p Path) String() string {
	var b strings.Builder
	for i, s := range p {
		switch {
		case s.IsIndex:
			b.WriteString("[" + strconv.Itoa(s.Index) + "]")
		case identifier.MatchString(s.Key):
			if i > 0 {
				b.WriteByte('.')
			}
			b.WriteString(s.Key)
		default:
			b.WriteString("[" + strconv.Quote(s.Key) + "]")
		}
	}
	return b.String()
}

// BracketString renders the path with every key after the first in quoted
// brackets, for example tags["Name"] where String gives tags.Name. Both forms
// are valid Terraform syntax for a map key, so ignore rules accept either.
func (p Path) BracketString() string {
	var b strings.Builder
	for i, s := range p {
		switch {
		case s.IsIndex:
			b.WriteString("[" + strconv.Itoa(s.Index) + "]")
		case i == 0 && identifier.MatchString(s.Key):
			b.WriteString(s.Key)
		default:
			b.WriteString("[" + strconv.Quote(s.Key) + "]")
		}
	}
	return b.String()
}

// HasPrefix reports whether q is a prefix of p (or equal to it).
func (p Path) HasPrefix(q Path) bool {
	if len(q) > len(p) {
		return false
	}
	for i := range q {
		if p[i] != q[i] {
			return false
		}
	}
	return true
}

// PathFromJSON converts one element of replace_paths, which is a JSON array of
// strings (keys) and numbers (indexes), into a Path.
func PathFromJSON(raw []any) (Path, error) {
	out := make(Path, 0, len(raw))
	for _, el := range raw {
		switch v := el.(type) {
		case string:
			out = append(out, KeyStep(v))
		case json.Number:
			n, err := strconv.Atoi(v.String())
			if err != nil {
				return nil, fmt.Errorf("invalid path index %q", v)
			}
			out = append(out, IndexStep(n))
		case float64:
			out = append(out, IndexStep(int(v)))
		default:
			return nil, fmt.Errorf("unsupported path element %v (%T)", el, el)
		}
	}
	return out, nil
}
