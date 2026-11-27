// Package tfplan decodes the machine-readable plan produced by
// "terraform show -json <planfile>".
//
// Only the parts of the format that drift detection needs are modelled. Values
// such as before, after and the sensitivity markers stay as generic JSON
// values because their shape depends on each provider's schema.
package tfplan

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// SupportedFormatMajor is the plan JSON format major version this package
// understands. Terraform promises backwards compatible changes within a major.
const SupportedFormatMajor = "1"

// Plan is the subset of the Terraform JSON plan representation used here.
type Plan struct {
	FormatVersion    string           `json:"format_version"`
	TerraformVersion string           `json:"terraform_version"`
	ResourceChanges  []ResourceChange `json:"resource_changes"`
	ResourceDrift    []ResourceChange `json:"resource_drift"`
	DeferredChanges  []DeferredChange `json:"deferred_changes"`
	Timestamp        string           `json:"timestamp"`
	Applyable        bool             `json:"applyable"`
	// Complete is nil for plans written before Terraform added the field
	// (format 1.2 and older releases); those plans are always complete.
	Complete *bool `json:"complete"`
	Errored  bool  `json:"errored"`

	// Variables holds the root module input values, and Configuration the
	// declarations that say which of them are sensitive.
	Variables     map[string]VariableValue `json:"variables"`
	Configuration Configuration            `json:"configuration"`
}

// VariableValue is one root module input variable value.
type VariableValue struct {
	Value any `json:"value"`
}

// Configuration is the part of the plan's configuration snapshot driftwatch
// reads: the root module's variable declarations.
type Configuration struct {
	RootModule struct {
		Variables map[string]VariableDeclaration `json:"variables"`
	} `json:"root_module"`
}

// VariableDeclaration is a root module variable block.
type VariableDeclaration struct {
	Sensitive bool `json:"sensitive"`
}

// SensitiveVariableValues returns the values of root module variables that
// are declared sensitive. The plan JSON records them in plain text.
func (p *Plan) SensitiveVariableValues() []any {
	var out []any
	for name, decl := range p.Configuration.RootModule.Variables {
		if v, ok := p.Variables[name]; ok && decl.Sensitive {
			out = append(out, v.Value)
		}
	}
	return out
}

// DeferredChange is a change Terraform could not plan yet, typically because
// count, for_each or a provider configuration depends on a value that is only
// known after apply. Terraform will plan it in a later run.
type DeferredChange struct {
	Reason         string         `json:"reason"`
	ResourceChange ResourceChange `json:"resource_change"`
}

// IsComplete reports whether applying the plan would converge the
// configuration in one run. A plan with deferred changes is incomplete: it
// shows only part of the drift.
func (p *Plan) IsComplete() bool {
	if p.Complete != nil && !*p.Complete {
		return false
	}
	return len(p.DeferredChanges) == 0
}

// ResourceChange describes the planned change for one resource instance.
type ResourceChange struct {
	Address         string `json:"address"`
	PreviousAddress string `json:"previous_address,omitempty"`
	ModuleAddress   string `json:"module_address,omitempty"`
	Mode            string `json:"mode"`
	Type            string `json:"type"`
	Name            string `json:"name"`
	Index           any    `json:"index,omitempty"`
	ProviderName    string `json:"provider_name"`
	Change          Change `json:"change"`
	ActionReason    string `json:"action_reason,omitempty"`
}

// Change holds the before and after values for a resource instance.
type Change struct {
	Actions         []string `json:"actions"`
	Before          any      `json:"before"`
	After           any      `json:"after"`
	AfterUnknown    any      `json:"after_unknown"`
	BeforeSensitive any      `json:"before_sensitive"`
	AfterSensitive  any      `json:"after_sensitive"`
	ReplacePaths    [][]any  `json:"replace_paths,omitempty"`
}

// Action is a single Terraform plan action.
type Action string

// Actions Terraform can emit in a plan.
const (
	ActionNoOp   Action = "no-op"
	ActionCreate Action = "create"
	ActionRead   Action = "read"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
	ActionForget Action = "forget"
)

// Parse decodes and validates a JSON plan.
func Parse(r io.Reader) (*Plan, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var p Plan
	if err := dec.Decode(&p); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("plan JSON is empty")
		}
		return nil, fmt.Errorf("decode plan JSON: %w", err)
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// ParseBytes is a convenience wrapper around Parse.
func ParseBytes(b []byte) (*Plan, error) {
	return Parse(strings.NewReader(string(b)))
}

func (p *Plan) validate() error {
	if p.FormatVersion == "" {
		return errors.New("plan JSON has no format_version; is this the output of \"terraform show -json <planfile>\"?")
	}
	major, _, _ := strings.Cut(p.FormatVersion, ".")
	if major != SupportedFormatMajor {
		return fmt.Errorf("unsupported plan format_version %q (want %s.x)", p.FormatVersion, SupportedFormatMajor)
	}
	if p.Errored {
		return errors.New("plan JSON is marked errored; Terraform could not produce a complete plan")
	}
	for i, rc := range p.ResourceChanges {
		if rc.Address == "" {
			return fmt.Errorf("resource_changes[%d] has no address", i)
		}
		if len(rc.Change.Actions) == 0 {
			return fmt.Errorf("resource %s has no change actions", rc.Address)
		}
	}
	for i, dc := range p.DeferredChanges {
		if dc.ResourceChange.Address == "" {
			return fmt.Errorf("deferred_changes[%d] has no address", i)
		}
	}
	return nil
}

// ActionList returns the actions of a change as typed values.
func (c Change) ActionList() []Action {
	out := make([]Action, len(c.Actions))
	for i, a := range c.Actions {
		out[i] = Action(a)
	}
	return out
}
