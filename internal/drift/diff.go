package drift

import (
	"reflect"
	"sort"
	"strconv"

	"github.com/builtbyshailendra/driftwatch/internal/tfplan"
)

// Attribute is one changed attribute of a resource. Values are masked before
// they are stored: a wholly sensitive side keeps no value at all, and a value
// that is only partly sensitive (an object with one secret key, say) keeps its
// structure with every sensitive element replaced by SensitivePlaceholder. A
// masked value therefore cannot leak into any renderer.
type Attribute struct {
	Path              string `json:"path"`
	Before            any    `json:"before"`
	After             any    `json:"after"`
	BeforeSensitive   bool   `json:"before_sensitive,omitempty"`
	AfterSensitive    bool   `json:"after_sensitive,omitempty"`
	ContainsSensitive bool   `json:"contains_sensitive,omitempty"`
	AfterUnknown      bool   `json:"after_unknown,omitempty"`
	ForcesReplacement bool   `json:"forces_replacement,omitempty"`

	path tfplan.Path
}

// SensitivePlaceholder replaces sensitive elements nested inside an otherwise
// visible value. It matches the wording Terraform uses in its own plan output.
const SensitivePlaceholder = "(sensitive value)"

// Sensitive reports whether any part of either side of the attribute is
// masked.
func (a Attribute) Sensitive() bool {
	return a.BeforeSensitive || a.AfterSensitive || a.ContainsSensitive
}

// ancestors returns the rendered path of the attribute and of each parent,
// outermost first. Each path appears in dotted form (tags.Name) and, when it
// differs, in bracket form (tags["Name"]), so that an ignore rule written in
// either style matches.
func (a Attribute) ancestors() []string {
	out := make([]string, 0, 2*len(a.path))
	for i := 1; i <= len(a.path); i++ {
		dotted, bracketed := a.path[:i].String(), a.path[:i].BracketString()
		out = append(out, dotted)
		if bracketed != dotted {
			out = append(out, bracketed)
		}
	}
	return out
}

// DiffAttributes compares the before and after values of a change and returns
// the attributes that differ, with sensitive values removed and unknown values
// flagged. replacePaths marks attributes that force replacement. secrets, from
// CollectSecrets, are masked wherever they reappear in values Terraform did
// not mark; it may be nil.
func DiffAttributes(c tfplan.Change, replacePaths []tfplan.Path, secrets *Secrets) []Attribute {
	d := differ{replace: replacePaths, secrets: secrets}
	d.walk(nil, c.Before, c.BeforeSensitive, c.After, c.AfterSensitive, c.AfterUnknown)
	sort.SliceStable(d.out, func(i, j int) bool { return d.out[i].Path < d.out[j].Path })
	return d.out
}

type differ struct {
	replace []tfplan.Path
	secrets *Secrets
	out     []Attribute
}

// walk descends both values in step. Sensitivity and unknown markers mirror
// the shape of the values: a boolean true marks the whole subtree, an object
// or array marks its children individually. Whenever walk stops descending it
// hands the markers of that subtree to emit, which masks what they cover.
func (d *differ) walk(path tfplan.Path, before, beforeSens, after, afterSens, afterUnk any) {
	if isTrue(beforeSens) || isTrue(afterSens) || isTrue(afterUnk) {
		if !isTrue(afterUnk) && reflect.DeepEqual(before, after) {
			return
		}
		d.emit(path, before, beforeSens, after, afterSens, afterUnk)
		return
	}

	bMap, bIsMap := before.(map[string]any)
	aMap, aIsMap := after.(map[string]any)
	if (bIsMap || before == nil) && (aIsMap || after == nil) && (bIsMap || aIsMap) {
		keys := unionKeys(bMap, aMap, unknownKeys(afterUnk))
		if len(keys) == 0 {
			if !reflect.DeepEqual(before, after) {
				d.emit(path, before, beforeSens, after, afterSens, afterUnk)
			}
			return
		}
		for _, k := range keys {
			d.walk(path.Append(tfplan.KeyStep(k)),
				bMap[k], child(beforeSens, k),
				aMap[k], child(afterSens, k), child(afterUnk, k))
		}
		return
	}

	bList, bIsList := before.([]any)
	aList, aIsList := after.([]any)
	if (bIsList || before == nil) && (aIsList || after == nil) && (bIsList || aIsList) {
		n := max(len(bList), len(aList))
		if u, ok := afterUnk.([]any); ok {
			n = max(n, len(u))
		}
		if n == 0 {
			if !reflect.DeepEqual(before, after) {
				d.emit(path, before, beforeSens, after, afterSens, afterUnk)
			}
			return
		}
		for i := 0; i < n; i++ {
			d.walk(path.Append(tfplan.IndexStep(i)),
				index(bList, i), childIndex(beforeSens, i),
				index(aList, i), childIndex(afterSens, i), childIndex(afterUnk, i))
		}
		return
	}

	// Scalars, or values whose type changed (an object replaced by a string,
	// for example). The markers may still describe a nested shape, so emit
	// masks with them rather than assuming the values are plain.
	if !reflect.DeepEqual(before, after) {
		d.emit(path, before, beforeSens, after, afterSens, afterUnk)
	}
}

// emit records one changed attribute. The marker arguments are the
// before_sensitive, after_sensitive and after_unknown subtrees for path.
func (d *differ) emit(path tfplan.Path, before, beforeSens, after, afterSens, afterUnk any) {
	a := Attribute{
		Path:            path.String(),
		BeforeSensitive: isTrue(beforeSens),
		AfterSensitive:  isTrue(afterSens),
		AfterUnknown:    isTrue(afterUnk),
		path:            path,
	}
	if a.Path == "" {
		a.Path = "(resource)"
	}
	if !a.BeforeSensitive {
		a.Before = d.mask(&a, before, beforeSens)
	}
	if !a.AfterSensitive && !a.AfterUnknown {
		a.After = d.mask(&a, after, afterSens)
	}
	for _, rp := range d.replace {
		if path.HasPrefix(rp) || rp.HasPrefix(path) {
			a.ForcesReplacement = true
			break
		}
	}
	d.out = append(d.out, a)
}

// mask applies the sensitivity marker and then the known secrets to one side
// of an attribute, recording in a whether anything was replaced.
func (d *differ) mask(a *Attribute, v, marker any) any {
	v, marked := Mask(v, marker)
	v, scrubbed := d.secrets.Scrub(v)
	if marked || scrubbed {
		a.ContainsSensitive = true
	}
	return v
}

// Mask returns a copy of v in which every element that the sensitivity
// marker covers is replaced by SensitivePlaceholder, and reports whether
// anything was replaced. marker follows the plan JSON convention: true masks
// the whole value, an object or array masks matching children, and false or
// nil masks nothing.
//
// Mask fails closed. When the marker has a nested shape that does not fit the
// value (an object marker on a list, or on a string) and the marker covers
// anything at all, the whole value is masked, because there is no way to tell
// which part of it is secret.
func Mask(v, marker any) (any, bool) {
	if !markerCovers(marker) || v == nil {
		return v, false
	}
	if isTrue(marker) {
		return SensitivePlaceholder, true
	}
	switch m := marker.(type) {
	case map[string]any:
		obj, ok := v.(map[string]any)
		if !ok {
			return SensitivePlaceholder, true
		}
		out := make(map[string]any, len(obj))
		masked := false
		for k, el := range obj {
			mv, mk := Mask(el, m[k])
			out[k] = mv
			masked = masked || mk
		}
		return out, masked
	case []any:
		list, ok := v.([]any)
		if !ok {
			return SensitivePlaceholder, true
		}
		out := make([]any, len(list))
		masked := false
		for i, el := range list {
			mv, mk := Mask(el, childIndex(m, i))
			out[i] = mv
			masked = masked || mk
		}
		return out, masked
	default:
		// A marker of any other type is not something Terraform emits.
		return SensitivePlaceholder, true
	}
}

// markerCovers reports whether a sensitivity marker marks anything at all.
func markerCovers(marker any) bool {
	switch m := marker.(type) {
	case nil:
		return false
	case bool:
		return m
	case map[string]any:
		for _, el := range m {
			if markerCovers(el) {
				return true
			}
		}
		return false
	case []any:
		for _, el := range m {
			if markerCovers(el) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func isTrue(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

func child(v any, key string) any {
	if m, ok := v.(map[string]any); ok {
		return m[key]
	}
	return nil
}

func childIndex(v any, i int) any {
	if l, ok := v.([]any); ok && i < len(l) {
		return l[i]
	}
	return nil
}

func index(l []any, i int) any {
	if i < len(l) {
		return l[i]
	}
	return nil
}

// unknownKeys returns the keys of an after_unknown object that mark a value
// as unknown. Unknown attributes are absent from "after", so they would be
// missed without this.
func unknownKeys(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, u := range m {
		if isTrue(u) {
			out[k] = true
		}
	}
	return out
}

func unionKeys(maps ...map[string]any) []string {
	seen := map[string]struct{}{}
	for _, m := range maps {
		for k := range m {
			seen[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		// Numeric-looking keys sort numerically so "10" follows "9".
		ni, ei := strconv.Atoi(keys[i])
		nj, ej := strconv.Atoi(keys[j])
		if ei == nil && ej == nil {
			return ni < nj
		}
		return keys[i] < keys[j]
	})
	return keys
}
