package report

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/builtbyshailendra/driftwatch/internal/drift"
	"github.com/builtbyshailendra/driftwatch/internal/ignore"
	"github.com/builtbyshailendra/driftwatch/internal/tfplan"
)

var update = flag.Bool("update", false, "rewrite golden files")

var fixedTime = time.Date(2026, 11, 7, 6, 0, 0, 0, time.UTC)

func buildReport(t *testing.T, planFile string, rules *ignore.Rules) *Report {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "plans", planFile))
	if err != nil {
		t.Fatal(err)
	}
	p, err := tfplan.ParseBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	res, err := drift.Analyze(p, rules)
	if err != nil {
		t.Fatal(err)
	}
	return New(res, "drift-demo", fixedTime)
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/report -update to create it)", err)
	}
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from golden file; run go test ./internal/report -update and review the diff\n--- got ---\n%s", name, got)
	}
}

func TestGoldenReports(t *testing.T) {
	rules := ignore.New(
		ignore.Rule{Address: `module.service["worker"].*`},
		ignore.Rule{Address: "terraform_data.metadata", Attribute: "input.owner"},
	)
	tests := []struct {
		name  string
		plan  string
		rules *ignore.Rules
	}{
		{"drift", "drift.json", nil},
		{"drift-ignored", "drift.json", rules},
		{"clean", "clean.json", nil},
		{"refresh-only", "refresh-only.json", nil},
		{"sensitive", "sensitive.json", nil},
		{"deferred", "deferred.json", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := buildReport(t, tt.plan, tt.rules)
			var md, js bytes.Buffer
			if err := r.WriteMarkdown(&md); err != nil {
				t.Fatal(err)
			}
			if err := r.WriteJSON(&js); err != nil {
				t.Fatal(err)
			}
			golden(t, tt.name+".md", md.Bytes())
			golden(t, tt.name+".json", js.Bytes())
		})
	}
}

func TestSummary(t *testing.T) {
	r := buildReport(t, "drift.json", ignore.New(ignore.Rule{Address: "random_pet.*"}))
	want := Summary{Delete: 3, Replace: 4, Update: 1, Create: 1, Outside: 1, Ignored: 1, Total: 9}
	if r.Summary != want {
		t.Errorf("summary = %+v, want %+v", r.Summary, want)
	}
	if !r.HasDrift || r.SchemaVersion != SchemaVersion || r.Workspace != "drift-demo" {
		t.Errorf("report header = %+v", r)
	}
}

func TestJSONReportShape(t *testing.T) {
	r := buildReport(t, "drift.json", nil)
	var buf bytes.Buffer
	if err := r.WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema_version", "workspace", "generated_at", "terraform_version", "has_drift", "summary", "changes", "outside_changes", "ignored"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("JSON report lacks %q", key)
		}
	}
	if decoded["generated_at"] != "2026-11-07T06:00:00Z" {
		t.Errorf("generated_at = %v", decoded["generated_at"])
	}
	clean := buildReport(t, "clean.json", nil)
	buf.Reset()
	if err := clean.WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"changes": []`) {
		t.Error("clean report should encode changes as [] rather than null")
	}
}

func TestHeadline(t *testing.T) {
	tests := []struct {
		name string
		r    Report
		want string
	}{
		{"clean", Report{}, "no drift"},
		{"one kind", Report{HasDrift: true, Summary: Summary{Update: 2}}, "2 to update"},
		{"mixed", Report{HasDrift: true, Summary: Summary{Delete: 1, Create: 3, Outside: 1}}, "1 to delete, 3 to create, 1 changed outside Terraform"},
		{"incomplete", Report{HasDrift: true, PlanIncomplete: true, Summary: Summary{Update: 1, Deferred: 2}}, "1 to update (plan incomplete, 2 deferred)"},
		{"incomplete without drift", Report{PlanIncomplete: true, Summary: Summary{Deferred: 1}}, "no drift (plan incomplete, 1 deferred)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.r.Headline(); got != tt.want {
				t.Errorf("Headline() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatValue(t *testing.T) {
	tests := []struct {
		name               string
		v                  any
		sensitive, unknown bool
		want               string
	}{
		{"string", "a<b>&c", false, false, `"a<b>&c"`},
		{"number", json.Number("42"), false, false, "42"},
		{"null", nil, false, false, "null"},
		{"map", map[string]any{"b": 1, "a": true}, false, false, `{"a":true,"b":1}`},
		{"newline stays escaped", "line1\nline2", false, false, `"line1\nline2"`},
		{"sensitive wins over unknown", "secret", true, true, "(sensitive value)"},
		{"unknown", nil, false, true, "(known after apply)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatValue(tt.v, tt.sensitive, tt.unknown); got != tt.want {
				t.Errorf("FormatValue = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTruncateAndCode(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is too long", 10, "this is..."},
		{"héllo wörld", 8, "héllo..."},
		{"abcdef", 2, "ab"},
	}
	for _, tt := range tests {
		if got := Truncate(tt.in, tt.n); got != tt.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
		}
	}
	codes := map[string]string{
		"plain":    "`plain`",
		"a|b":      "`a\\|b`",
		"has`tick": "`` has`tick ``",
	}
	for in, want := range codes {
		if got := code(in); got != want {
			t.Errorf("code(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderedReportsContainNoSecrets(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "plans", "drift.json"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := tfplan.ParseBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	var secret string
	for _, rc := range p.ResourceChanges {
		if rc.Address == "random_password.database" {
			secret = rc.Change.Before.(map[string]any)["result"].(string)
		}
	}
	if len(secret) < 10 {
		t.Fatal("fixture secret not found")
	}
	r := buildReport(t, "drift.json", nil)
	var js bytes.Buffer
	if err := r.WriteJSON(&js); err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{"markdown": r.Markdown(), "json": js.String()} {
		if strings.Contains(out, secret) {
			t.Errorf("%s report contains the database password", name)
		}
		if !strings.Contains(out, "sensitive") {
			t.Errorf("%s report does not flag sensitive attributes", name)
		}
	}
}

// TestSensitivePlanRendersNoSecret renders the real plan in which objects with
// a sensitive key become unknown or change type, and checks both formats.
func TestSensitivePlanRendersNoSecret(t *testing.T) {
	const secret = "fixture-only-Qv7rTz29mWk"
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "plans", "sensitive.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), secret) {
		t.Fatal("fixture no longer contains the secret; update the test")
	}
	r := buildReport(t, "sensitive.json", nil)
	var js bytes.Buffer
	if err := r.WriteJSON(&js); err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{"markdown": r.Markdown(), "json": js.String()} {
		if strings.Contains(out, secret) {
			t.Errorf("%s report contains the secret", name)
		}
		if !strings.Contains(out, `"user":"admin"`) && !strings.Contains(out, `"user": "admin"`) {
			t.Errorf("%s report lost the non-sensitive part of the value", name)
		}
	}
}

func TestDeferredPlanWarns(t *testing.T) {
	r := buildReport(t, "deferred.json", nil)
	if !r.PlanIncomplete || r.Summary.Deferred != 2 || len(r.Deferred) != 2 {
		t.Fatalf("report = %+v", r)
	}
	md := r.Markdown()
	for _, want := range []string{"**Warning:** this plan is incomplete", "deferred 2 changes", "## Deferred changes", "`kubernetes_namespace.apps`"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q", want)
		}
	}
	clean := buildReport(t, "clean.json", nil)
	if clean.PlanIncomplete || strings.Contains(clean.Markdown(), "incomplete") {
		t.Error("complete plan rendered as incomplete")
	}
}

func TestUnusedRulesInJSONReport(t *testing.T) {
	r := buildReport(t, "update-only.json", ignore.New(
		ignore.Rule{Address: "terraform_data.metadata", Attribute: "input.owner", Line: 1},
		ignore.Rule{Address: "aws_instance.*", Attribute: "tags", Line: 4},
		ignore.Rule{Address: "random_pet.*"},
	))
	want := []string{"line 4: aws_instance.* tags", "random_pet.*"}
	if strings.Join(r.UnusedIgnoreRules, "|") != strings.Join(want, "|") {
		t.Errorf("unused rules = %q, want %q", r.UnusedIgnoreRules, want)
	}
}
