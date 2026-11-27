// Package ignore parses driftwatch ignore files and matches resource
// addresses and attribute paths against them.
//
// The file format is line oriented, in the spirit of .gitignore:
//
//	# comments start with a hash
//	random_pet.*                          # ignore every random_pet resource
//	terraform_data.metadata  input.owner  # ignore one attribute
//	module.service["*"].*    triggers.*   # glob patterns in both columns
//	aws_instance.*  tags["LastScannedAt"] # map keys in dotted or bracket form
//
// The first field is a resource address pattern. The optional second field is
// an attribute path pattern. A rule without an attribute pattern ignores the
// whole resource; a rule with one ignores only the matching attributes.
//
// In patterns "*" matches any run of characters (including dots and brackets)
// and "?" matches exactly one character. An attribute pattern also matches
// every attribute nested below a matching path, so "tags" covers "tags.Name".
//
// Map keys may be written either way Terraform accepts: tags.Name and
// tags["Name"] are the same rule. Keys that are not identifiers, such as
// tags["kubernetes.io/role"], must use brackets, and tags["*"] matches any
// single key.
package ignore

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/builtbyshailendra/driftwatch/internal/tfplan"
)

// Rule is one parsed line of an ignore file.
type Rule struct {
	Address   string
	Attribute string
	Line      int
}

// attributeForms returns the attribute pattern as written and, when it
// differs, with every bracketed identifier key rewritten in dotted form
// (tags["Name"] becomes tags.Name). Callers render each attribute path in
// both dotted and bracket form, so between them a rule matches no matter
// which style it, or any part of it, was written in.
func (r Rule) attributeForms() []string {
	dotted := dottedPattern(r.Attribute)
	if dotted == r.Attribute {
		return []string{r.Attribute}
	}
	return []string{r.Attribute, dotted}
}

// dottedPattern rewrites ["key"] segments whose key is a plain identifier,
// without glob characters, into .key.
func dottedPattern(p string) string {
	var b strings.Builder
	for {
		start := strings.Index(p, `["`)
		if start < 0 {
			b.WriteString(p)
			return b.String()
		}
		end := strings.Index(p[start+2:], `"]`)
		if end < 0 {
			b.WriteString(p)
			return b.String()
		}
		key := p[start+2 : start+2+end]
		b.WriteString(p[:start])
		if tfplan.IsIdentifier(key) {
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(key)
		} else {
			b.WriteString(p[start : start+2+end+2])
		}
		p = p[start+2+end+2:]
	}
}

// String renders the rule the way it appears in the file.
func (r Rule) String() string {
	if r.Attribute == "" {
		return r.Address
	}
	return r.Address + " " + r.Attribute
}

// WholeResource reports whether the rule ignores an entire resource.
func (r Rule) WholeResource() bool { return r.Attribute == "" }

// Rules is an ordered rule set. The zero value ignores nothing.
type Rules struct {
	rules []Rule
}

// New builds a rule set from already constructed rules.
func New(rules ...Rule) *Rules { return &Rules{rules: rules} }

// Len returns the number of rules.
func (rs *Rules) Len() int {
	if rs == nil {
		return 0
	}
	return len(rs.rules)
}

// All returns a copy of the rules.
func (rs *Rules) All() []Rule {
	if rs == nil {
		return nil
	}
	return append([]Rule(nil), rs.rules...)
}

// Parse reads rules from r.
func Parse(r io.Reader) (*Rules, error) {
	sc := bufio.NewScanner(r)
	var rules []Rule
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		if i := strings.Index(text, "#"); i >= 0 && !insideQuotes(text, i) {
			text = text[:i]
		}
		fields := strings.Fields(text)
		switch len(fields) {
		case 0:
			continue
		case 1:
			rules = append(rules, Rule{Address: fields[0], Line: line})
		case 2:
			rules = append(rules, Rule{Address: fields[0], Attribute: fields[1], Line: line})
		default:
			return nil, fmt.Errorf("line %d: expected \"<address> [attribute]\", got %d fields", line, len(fields))
		}
		if err := validatePattern(rules[len(rules)-1].Address); err != nil {
			return nil, fmt.Errorf("line %d: address pattern: %w", line, err)
		}
		if err := validatePattern(rules[len(rules)-1].Attribute); err != nil {
			return nil, fmt.Errorf("line %d: attribute pattern: %w", line, err)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read ignore rules: %w", err)
	}
	return &Rules{rules: rules}, nil
}

// Load reads rules from a file. An empty path yields an empty rule set.
func Load(path string) (*Rules, error) {
	if path == "" {
		return &Rules{}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open ignore file: %w", err)
	}
	defer f.Close()
	rs, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return rs, nil
}

// MatchResource returns the first rule that ignores the whole resource.
func (rs *Rules) MatchResource(address string) (Rule, bool) {
	if rs == nil {
		return Rule{}, false
	}
	for _, r := range rs.rules {
		if r.WholeResource() && Glob(r.Address, address) {
			return r, true
		}
	}
	return Rule{}, false
}

// MatchAttribute returns the first attribute rule that covers the attribute.
// ancestors must list the attribute path and each of its parent paths in
// rendered form, ideally in both dotted and bracket form, for example
// ["tags", "tags.Name", `tags["Name"]`].
func (rs *Rules) MatchAttribute(address string, ancestors []string) (Rule, bool) {
	if rs == nil {
		return Rule{}, false
	}
	for _, r := range rs.rules {
		if r.WholeResource() || !Glob(r.Address, address) {
			continue
		}
		for _, pattern := range r.attributeForms() {
			for _, p := range ancestors {
				if Glob(pattern, p) {
					return r, true
				}
			}
		}
	}
	return Rule{}, false
}

// Unused returns the rules, in file order, that are not in used. The
// analysis uses it to point out rules that matched nothing in a plan.
func (rs *Rules) Unused(used map[Rule]bool) []Rule {
	if rs == nil {
		return nil
	}
	var out []Rule
	for _, r := range rs.rules {
		if !used[r] {
			out = append(out, r)
		}
	}
	return out
}

// Glob matches s against a pattern in which "*" matches any sequence of
// characters and "?" matches a single character. Every other character,
// including dots, brackets and quotes, matches itself.
func Glob(pattern, s string) bool {
	p, n := []rune(pattern), []rune(s)
	pi, ni := 0, 0
	star, mark := -1, 0
	for ni < len(n) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == n[ni]):
			pi++
			ni++
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, ni
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			ni = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

func validatePattern(p string) error {
	if strings.Count(p, `"`)%2 != 0 {
		return fmt.Errorf("unbalanced quotes in %q", p)
	}
	if strings.Count(p, "[") != strings.Count(p, "]") {
		return fmt.Errorf("unbalanced brackets in %q", p)
	}
	return nil
}

// insideQuotes reports whether byte offset i of s is inside a double-quoted
// span, so that "#" in a map key such as tags["#team"] is not a comment.
func insideQuotes(s string, i int) bool {
	return strings.Count(s[:i], `"`)%2 == 1
}
