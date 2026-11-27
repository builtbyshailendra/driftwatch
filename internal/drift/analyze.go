package drift

import (
	"fmt"
	"sort"

	"github.com/builtbyshailendra/driftwatch/internal/ignore"
	"github.com/builtbyshailendra/driftwatch/internal/tfplan"
)

// Source says where in the plan a change was found.
type Source string

const (
	// SourcePlan changes come from resource_changes: applying the plan would
	// make them to bring infrastructure back in line with configuration.
	SourcePlan Source = "plan"
	// SourceOutside changes come from resource_drift: Terraform noticed during
	// refresh that the object changed outside Terraform.
	SourceOutside Source = "outside"
)

// IgnoredAttribute is an attribute change suppressed by a rule.
type IgnoredAttribute struct {
	Path string `json:"path"`
	Rule string `json:"rule"`
}

// Change is one classified resource change.
type Change struct {
	Address           string             `json:"address"`
	ModuleAddress     string             `json:"module_address,omitempty"`
	Type              string             `json:"type"`
	Name              string             `json:"name"`
	Provider          string             `json:"provider"`
	Source            Source             `json:"source"`
	Kind              Kind               `json:"kind"`
	Severity          Severity           `json:"severity"`
	Actions           []string           `json:"actions"`
	Reason            string             `json:"reason,omitempty"`
	Attributes        []Attribute        `json:"attributes,omitempty"`
	IgnoredAttributes []IgnoredAttribute `json:"ignored_attributes,omitempty"`
}

// Ignored records a change that rules suppressed entirely.
type Ignored struct {
	Address string   `json:"address"`
	Source  Source   `json:"source"`
	Kind    Kind     `json:"kind"`
	Rules   []string `json:"rules"`
}

// Deferred is a change Terraform postponed to a later plan.
type Deferred struct {
	Address string   `json:"address"`
	Actions []string `json:"actions"`
	Reason  string   `json:"reason"`
}

// Result is the outcome of analysing one plan.
type Result struct {
	TerraformVersion string     `json:"terraform_version"`
	Incomplete       bool       `json:"incomplete,omitempty"`
	Changes          []Change   `json:"changes"`
	Outside          []Change   `json:"outside_changes"`
	Ignored          []Ignored  `json:"ignored"`
	Deferred         []Deferred `json:"deferred"`
	// UnusedRules lists ignore rules that matched nothing in this plan. That
	// is normal for a rule whose attribute did not change this time, and a
	// useful hint when a rule was expected to match.
	UnusedRules []ignore.Rule `json:"-"`
}

// Addresses returns the number of distinct resource addresses with drift.
// A resource can appear twice, once as changed outside Terraform and once
// as the planned change that reverts it, and is counted once.
func (r *Result) Addresses() int {
	seen := map[string]struct{}{}
	for _, cs := range [][]Change{r.Changes, r.Outside} {
		for _, c := range cs {
			seen[c.Address] = struct{}{}
		}
	}
	return len(seen)
}

// HasDrift reports whether any change survived the ignore rules.
func (r *Result) HasDrift() bool {
	return len(r.Changes) > 0 || len(r.Outside) > 0
}

// Counts returns the number of planned changes per kind. Every kind is
// present, with zero when there are none.
func (r *Result) Counts() map[Kind]int {
	out := make(map[Kind]int, len(KindOrder))
	for _, k := range KindOrder {
		out[k] = 0
	}
	for _, c := range r.Changes {
		out[c.Kind]++
	}
	return out
}

// Analyze classifies every managed-resource change in the plan and applies
// the ignore rules. A nil rule set ignores nothing.
func Analyze(p *tfplan.Plan, rules *ignore.Rules) (*Result, error) {
	res := &Result{
		TerraformVersion: p.TerraformVersion,
		Incomplete:       !p.IsComplete(),
		Changes:          []Change{},
		Outside:          []Change{},
		Ignored:          []Ignored{},
		Deferred:         []Deferred{},
	}
	used := map[ignore.Rule]bool{}
	secrets := CollectSecrets(p)
	sources := []struct {
		source  Source
		changes []tfplan.ResourceChange
		into    *[]Change
	}{
		{SourcePlan, p.ResourceChanges, &res.Changes},
		{SourceOutside, p.ResourceDrift, &res.Outside},
	}
	for _, src := range sources {
		for _, rc := range src.changes {
			if !isManaged(rc) {
				continue
			}
			c, ok, err := buildChange(rc, src.source, secrets)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			if ig, suppressed := applyRules(&c, rules, used); suppressed {
				res.Ignored = append(res.Ignored, ig)
				continue
			}
			*src.into = append(*src.into, c)
		}
	}
	for _, dc := range p.DeferredChanges {
		if !isManaged(dc.ResourceChange) {
			continue
		}
		res.Deferred = append(res.Deferred, Deferred{
			Address: dc.ResourceChange.Address,
			Actions: append([]string(nil), dc.ResourceChange.Change.Actions...),
			Reason:  DescribeDeferredReason(dc.Reason),
		})
	}
	res.UnusedRules = rules.Unused(used)
	sortChanges(res.Changes)
	sortChanges(res.Outside)
	sort.SliceStable(res.Deferred, func(i, j int) bool { return res.Deferred[i].Address < res.Deferred[j].Address })
	sort.SliceStable(res.Ignored, func(i, j int) bool {
		if res.Ignored[i].Source != res.Ignored[j].Source {
			return res.Ignored[i].Source > res.Ignored[j].Source // plan before outside
		}
		return res.Ignored[i].Address < res.Ignored[j].Address
	})
	return res, nil
}

func buildChange(rc tfplan.ResourceChange, src Source, secrets *Secrets) (Change, bool, error) {
	kind, ok, err := Classify(rc.Change.Actions)
	if err != nil {
		return Change{}, false, fmt.Errorf("%s: %w", rc.Address, err)
	}
	if !ok {
		return Change{}, false, nil
	}
	c := Change{
		Address:       rc.Address,
		ModuleAddress: rc.ModuleAddress,
		Type:          rc.Type,
		Name:          rc.Name,
		Provider:      rc.ProviderName,
		Source:        src,
		Kind:          kind,
		Severity:      kind.Severity(),
		Actions:       append([]string(nil), rc.Change.Actions...),
	}
	if rc.ActionReason != "" {
		c.Reason = DescribeReason(rc.ActionReason)
	}
	if kind == KindUpdate || kind == KindReplace {
		paths := make([]tfplan.Path, 0, len(rc.Change.ReplacePaths))
		for _, raw := range rc.Change.ReplacePaths {
			p, err := tfplan.PathFromJSON(raw)
			if err != nil {
				return Change{}, false, fmt.Errorf("%s: replace_paths: %w", rc.Address, err)
			}
			paths = append(paths, p)
		}
		c.Attributes = DiffAttributes(rc.Change, paths, secrets)
	}
	return c, true, nil
}

// applyRules removes ignored attributes from c. It returns true when the
// whole change should be suppressed: either a resource rule matched, or every
// attribute with a known new value was ignored. Attributes that merely become
// unknown are consequences of a change rather than causes, so they do not
// keep an otherwise ignored change alive.
func applyRules(c *Change, rules *ignore.Rules, used map[ignore.Rule]bool) (Ignored, bool) {
	if r, ok := rules.MatchResource(c.Address); ok {
		used[r] = true
		return Ignored{Address: c.Address, Source: c.Source, Kind: c.Kind, Rules: []string{r.String()}}, true
	}
	if len(c.Attributes) == 0 {
		return Ignored{}, false
	}
	kept := make([]Attribute, 0, len(c.Attributes))
	matched := map[string]struct{}{}
	known, knownIgnored := 0, 0
	for _, a := range c.Attributes {
		if !a.AfterUnknown {
			known++
		}
		if r, ok := rules.MatchAttribute(c.Address, a.ancestors()); ok {
			used[r] = true
			c.IgnoredAttributes = append(c.IgnoredAttributes, IgnoredAttribute{Path: a.Path, Rule: r.String()})
			matched[r.String()] = struct{}{}
			if !a.AfterUnknown {
				knownIgnored++
			}
			continue
		}
		kept = append(kept, a)
	}
	c.Attributes = kept
	if known > 0 && known == knownIgnored {
		names := make([]string, 0, len(matched))
		for m := range matched {
			names = append(names, m)
		}
		sort.Strings(names)
		return Ignored{Address: c.Address, Source: c.Source, Kind: c.Kind, Rules: names}, true
	}
	return Ignored{}, false
}

func sortChanges(cs []Change) {
	sort.SliceStable(cs, func(i, j int) bool {
		if ri, rj := cs[i].Kind.rank(), cs[j].Kind.rank(); ri != rj {
			return ri < rj
		}
		return cs[i].Address < cs[j].Address
	})
}
