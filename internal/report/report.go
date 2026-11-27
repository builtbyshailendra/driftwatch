// Package report renders drift analysis results as Markdown and JSON.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/builtbyshailendra/driftwatch/internal/drift"
	"github.com/builtbyshailendra/driftwatch/internal/ignore"
)

// SchemaVersion is bumped whenever the JSON report changes incompatibly.
const SchemaVersion = 1

// Summary counts changes per kind. Total is the number of distinct resources
// with drift: a resource changed outside Terraform that the plan would also
// revert appears in Outside and in one of the kinds, but only once in Total.
type Summary struct {
	Delete  int `json:"delete"`
	Replace int `json:"replace"`
	Update  int `json:"update"`
	Create  int `json:"create"`
	Outside int `json:"outside"`
	Ignored int `json:"ignored"`
	Total   int `json:"total"`
	// Deferred counts changes Terraform postponed to a later plan.
	Deferred int `json:"deferred"`
}

// Report is the serialisable result of one drift check.
type Report struct {
	SchemaVersion    int             `json:"schema_version"`
	Workspace        string          `json:"workspace"`
	GeneratedAt      time.Time       `json:"generated_at"`
	TerraformVersion string          `json:"terraform_version"`
	HasDrift         bool            `json:"has_drift"`
	PlanIncomplete   bool            `json:"plan_incomplete,omitempty"`
	Summary          Summary         `json:"summary"`
	Changes          []drift.Change  `json:"changes"`
	OutsideChanges   []drift.Change  `json:"outside_changes"`
	Ignored          []drift.Ignored `json:"ignored"`
	// Deferred lists changes Terraform could not plan yet. It is present
	// only for incomplete plans.
	Deferred []drift.Deferred `json:"deferred_changes,omitempty"`
	// UnusedIgnoreRules lists rules that matched nothing in this plan.
	UnusedIgnoreRules []string `json:"unused_ignore_rules,omitempty"`
}

// New builds a report from an analysis result.
func New(res *drift.Result, workspace string, generatedAt time.Time) *Report {
	counts := res.Counts()
	s := Summary{
		Delete:   counts[drift.KindDelete],
		Replace:  counts[drift.KindReplace],
		Update:   counts[drift.KindUpdate],
		Create:   counts[drift.KindCreate],
		Outside:  len(res.Outside),
		Ignored:  len(res.Ignored),
		Total:    res.Addresses(),
		Deferred: len(res.Deferred),
	}
	var unused []string
	for _, r := range res.UnusedRules {
		unused = append(unused, describeRule(r))
	}
	rep := &Report{
		SchemaVersion:     SchemaVersion,
		Workspace:         workspace,
		GeneratedAt:       generatedAt.UTC().Truncate(time.Second),
		TerraformVersion:  res.TerraformVersion,
		HasDrift:          res.HasDrift(),
		PlanIncomplete:    res.Incomplete,
		Summary:           s,
		Changes:           res.Changes,
		OutsideChanges:    res.Outside,
		Ignored:           res.Ignored,
		UnusedIgnoreRules: unused,
	}
	if len(res.Deferred) > 0 {
		rep.Deferred = res.Deferred
	}
	return rep
}

func describeRule(r ignore.Rule) string {
	if r.Line > 0 {
		return fmt.Sprintf("line %d: %s", r.Line, r.String())
	}
	return r.String()
}

// WriteJSON writes the report as indented JSON.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return fmt.Errorf("encode JSON report: %w", err)
	}
	return nil
}

// Headline summarises the drift in one line, for example
// "1 to delete, 2 to replace, 1 changed outside Terraform". An incomplete
// plan says so, because it may hide drift.
func (r *Report) Headline() string {
	h := r.driftHeadline()
	if r.PlanIncomplete {
		h += fmt.Sprintf(" (plan incomplete, %d deferred)", r.Summary.Deferred)
	}
	return h
}

func (r *Report) driftHeadline() string {
	if !r.HasDrift {
		return "no drift"
	}
	var parts []string
	for _, p := range []struct {
		n    int
		verb string
	}{
		{r.Summary.Delete, "to delete"},
		{r.Summary.Replace, "to replace"},
		{r.Summary.Update, "to update"},
		{r.Summary.Create, "to create"},
		{r.Summary.Outside, "changed outside Terraform"},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.verb))
		}
	}
	return strings.Join(parts, ", ")
}

// Title is used for GitHub issues and Slack headers.
func (r *Report) Title() string {
	return "Terraform drift detected in " + r.Workspace
}

// FormatValue renders an attribute value for humans. Sensitive and unknown
// values are replaced by markers; everything else is compact JSON.
func FormatValue(v any, sensitive, unknown bool) string {
	switch {
	case sensitive:
		return "(sensitive value)"
	case unknown:
		return "(known after apply)"
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprintf("%v", v)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
