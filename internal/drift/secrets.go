package drift

import (
	"sort"
	"strings"

	"github.com/builtbyshailendra/driftwatch/internal/tfplan"
)

// MinEmbeddedSecretLength is the shortest secret that is also searched for
// inside longer strings. Shorter secrets are masked only where a whole value
// equals them, so a four-character secret does not blank out every string
// that happens to contain those characters.
const MinEmbeddedSecretLength = 8

// Secrets holds string values the plan marks sensitive somewhere. Terraform
// tracks sensitivity per value, not per string, so the same secret can show
// up unmarked in another attribute: terraform_data copies input to output
// without the marks, and a provider may echo a password into a connection
// string. Scrub masks those copies as a second line of defence behind Mask.
type Secrets struct {
	exact    map[string]struct{}
	embedded []string // longest first, so overlapping secrets mask fully
}

// CollectSecrets gathers every string the plan marks sensitive: values under
// before_sensitive and after_sensitive markers in resource_changes,
// resource_drift and deferred_changes, and root variables declared sensitive.
func CollectSecrets(p *tfplan.Plan) *Secrets {
	s := &Secrets{exact: map[string]struct{}{}}
	changes := make([]tfplan.ResourceChange, 0, len(p.ResourceChanges)+len(p.ResourceDrift)+len(p.DeferredChanges))
	changes = append(changes, p.ResourceChanges...)
	changes = append(changes, p.ResourceDrift...)
	for _, dc := range p.DeferredChanges {
		changes = append(changes, dc.ResourceChange)
	}
	for _, rc := range changes {
		s.addMarked(rc.Change.Before, rc.Change.BeforeSensitive, false)
		s.addMarked(rc.Change.After, rc.Change.AfterSensitive, false)
	}
	for _, v := range p.SensitiveVariableValues() {
		s.addMarked(v, true, true)
	}
	for k := range s.exact {
		if len(k) >= MinEmbeddedSecretLength {
			s.embedded = append(s.embedded, k)
		}
	}
	sort.Slice(s.embedded, func(i, j int) bool {
		if len(s.embedded[i]) != len(s.embedded[j]) {
			return len(s.embedded[i]) > len(s.embedded[j])
		}
		return s.embedded[i] < s.embedded[j]
	})
	return s
}

// Len returns the number of distinct secrets.
func (s *Secrets) Len() int {
	if s == nil {
		return 0
	}
	return len(s.exact)
}

func (s *Secrets) addMarked(v, marker any, covered bool) {
	covered = covered || isTrue(marker)
	switch val := v.(type) {
	case map[string]any:
		for k, el := range val {
			s.addMarked(el, child(marker, k), covered)
		}
	case []any:
		for i, el := range val {
			s.addMarked(el, childIndex(marker, i), covered)
		}
	case string:
		// Surrounding whitespace is dropped so that "secret\n" in a file
		// body also finds "secret" elsewhere.
		if t := strings.TrimSpace(val); covered && t != "" {
			s.exact[val] = struct{}{}
			s.exact[t] = struct{}{}
		}
	}
}

// Scrub returns a copy of v in which every string equal to a secret is
// replaced by SensitivePlaceholder, and every secret of at least
// MinEmbeddedSecretLength characters inside a longer string is replaced in
// place. It reports whether anything changed.
func (s *Secrets) Scrub(v any) (any, bool) {
	if s.Len() == 0 {
		return v, false
	}
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		changed := false
		for k, el := range val {
			sv, c := s.Scrub(el)
			out[k] = sv
			changed = changed || c
		}
		return out, changed
	case []any:
		out := make([]any, len(val))
		changed := false
		for i, el := range val {
			sv, c := s.Scrub(el)
			out[i] = sv
			changed = changed || c
		}
		return out, changed
	case string:
		if _, ok := s.exact[val]; ok {
			return SensitivePlaceholder, true
		}
		out := val
		for _, secret := range s.embedded {
			out = strings.ReplaceAll(out, secret, SensitivePlaceholder)
		}
		return out, out != val
	default:
		return v, false
	}
}
