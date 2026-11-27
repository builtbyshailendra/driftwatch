// Package drift turns a Terraform JSON plan into a list of classified,
// sensitivity-masked changes, applying ignore rules along the way.
package drift

import (
	"fmt"
	"strings"

	"github.com/builtbyshailendra/driftwatch/internal/tfplan"
)

// Kind is the classification of a resource change.
type Kind string

// Change kinds. KindOrder ranks them from most to least disruptive.
const (
	KindDelete  Kind = "delete"
	KindReplace Kind = "replace"
	KindUpdate  Kind = "update"
	KindCreate  Kind = "create"
)

// KindOrder lists kinds from most to least disruptive. Reports use it for
// sorting and for summary tables.
var KindOrder = []Kind{KindDelete, KindReplace, KindUpdate, KindCreate}

// Severity ranks how disruptive a change is.
type Severity string

// Severities assigned to kinds.
const (
	SeverityHigh   Severity = "high"
	SeverityMedium Severity = "medium"
	SeverityLow    Severity = "low"
)

// Severity returns the severity of a kind: deletes and replacements destroy
// infrastructure, updates modify it in place, creates only add.
func (k Kind) Severity() Severity {
	switch k {
	case KindDelete, KindReplace:
		return SeverityHigh
	case KindUpdate:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

// rank orders kinds for sorting; lower is more disruptive.
func (k Kind) rank() int {
	for i, o := range KindOrder {
		if o == k {
			return i
		}
	}
	return len(KindOrder)
}

// Classify maps a Terraform action list to a Kind. The boolean is false for
// action lists that do not change infrastructure (no-op, read, forget).
func Classify(actions []string) (Kind, bool, error) {
	switch strings.Join(actions, ",") {
	case "no-op", "read", "forget":
		return "", false, nil
	case "create":
		return KindCreate, true, nil
	case "update":
		return KindUpdate, true, nil
	case "delete":
		return KindDelete, true, nil
	case "delete,create", "create,delete":
		return KindReplace, true, nil
	default:
		return "", false, fmt.Errorf("unrecognised action list %v", actions)
	}
}

var reasons = map[string]string{
	"replace_because_cannot_update":     "an attribute that forces replacement changed",
	"replace_because_tainted":           "the resource is tainted",
	"replace_by_request":                "replacement was requested with -replace",
	"replace_by_triggers":               "a replace_triggered_by reference changed",
	"delete_because_no_resource_config": "the resource block was removed from configuration",
	"delete_because_no_module":          "its module call was removed from configuration",
	"delete_because_wrong_repetition":   "count or for_each was added or removed",
	"delete_because_count_index":        "count no longer includes this index",
	"delete_because_each_key":           "for_each no longer includes this key",
	"delete_because_no_move_target":     "the moved block target does not exist",
}

var deferredReasons = map[string]string{
	"instance_count_unknown":  "count or for_each is known only after apply",
	"resource_config_unknown": "its configuration is known only after apply",
	"provider_config_unknown": "its provider configuration is known only after apply",
	"absent_prereq":           "a resource it depends on does not exist yet",
	"deferred_prereq":         "a resource it depends on was deferred",
	"unknown":                 "Terraform gave no reason",
}

// DescribeDeferredReason turns a deferred_changes reason into a short
// sentence fragment, returning unknown reasons verbatim.
func DescribeDeferredReason(reason string) string {
	if d, ok := deferredReasons[reason]; ok {
		return d
	}
	return reason
}

// DescribeReason turns a plan action_reason into a short sentence fragment.
// Unknown reasons are returned verbatim so new Terraform releases degrade
// gracefully.
func DescribeReason(reason string) string {
	if d, ok := reasons[reason]; ok {
		return d
	}
	return reason
}

// isManaged filters out data sources, which never drift.
func isManaged(rc tfplan.ResourceChange) bool {
	return rc.Mode == "" || rc.Mode == "managed"
}
