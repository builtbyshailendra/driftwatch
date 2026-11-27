package ignore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlob(t *testing.T) {
	tests := []struct {
		pattern, s string
		want       bool
	}{
		{"random_pet.environment", "random_pet.environment", true},
		{"random_pet.*", "random_pet.environment", true},
		{"random_pet.*", "random_password.database", false},
		{"*", "anything[0].at.all", true},
		{"", "", true},
		{"", "x", false},
		{"null_resource.cache_warmer[?]", "null_resource.cache_warmer[0]", true},
		{"null_resource.cache_warmer[?]", "null_resource.cache_warmer[10]", false},
		{`module.service["*"].*`, `module.service["worker"].null_resource.rollout`, true},
		{`module.service["api"].*`, `module.service["worker"].null_resource.rollout`, false},
		{"*.rollout", `module.service["api"].null_resource.rollout`, true},
		{"tags.*", "tags.Name", true},
		{"tags.*", "tags", false},
		{"a*b*c", "aXXbYYc", true},
		{"a*b*c", "aXXbYY", false},
		{"**", "x", true},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+"~"+tt.s, func(t *testing.T) {
			if got := Glob(tt.pattern, tt.s); got != tt.want {
				t.Errorf("Glob(%q, %q) = %v, want %v", tt.pattern, tt.s, got, tt.want)
			}
		})
	}
}

func TestParse(t *testing.T) {
	src := `
# full-line comment
random_pet.*

terraform_data.metadata   input.owner   # trailing comment
aws_s3_bucket.logs        tags["#team"]
`
	rs, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []Rule{
		{Address: "random_pet.*", Line: 3},
		{Address: "terraform_data.metadata", Attribute: "input.owner", Line: 5},
		{Address: "aws_s3_bucket.logs", Attribute: `tags["#team"]`, Line: 6},
	}
	got := rs.All()
	if len(got) != len(want) {
		t.Fatalf("got %d rules, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("rule %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if rs.Len() != 3 {
		t.Errorf("Len() = %d", rs.Len())
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{"too many fields", "a.b c d", "line 1: expected"},
		{"unbalanced quote", `module.x["a].*`, "unbalanced quotes"},
		{"unbalanced bracket", "null_resource.x[0", "unbalanced brackets"},
		{"bad attribute", "\n\na.b tags[\"x\"", "line 3: attribute pattern"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.src))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestMatchResource(t *testing.T) {
	rs := New(
		Rule{Address: "random_pet.*"},
		Rule{Address: "terraform_data.metadata", Attribute: "input.owner"},
		Rule{Address: `module.service["worker"].*`},
	)
	tests := []struct {
		address  string
		want     bool
		wantRule string
	}{
		{"random_pet.environment", true, "random_pet.*"},
		{"terraform_data.metadata", false, ""}, // attribute rules never ignore a whole resource
		{`module.service["worker"].random_integer.port`, true, `module.service["worker"].*`},
		{`module.service["api"].random_integer.port`, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			r, ok := rs.MatchResource(tt.address)
			if ok != tt.want || (ok && r.String() != tt.wantRule) {
				t.Errorf("MatchResource(%q) = %v, %v", tt.address, r, ok)
			}
		})
	}
}

func TestMatchAttribute(t *testing.T) {
	rs := New(
		Rule{Address: "terraform_data.metadata", Attribute: "input.owner"},
		Rule{Address: "null_resource.*", Attribute: "triggers"},
		Rule{Address: "aws_instance.web", Attribute: `tags["*"]`},
	)
	tests := []struct {
		address   string
		ancestors []string
		want      bool
	}{
		{"terraform_data.metadata", []string{"input", "input.owner"}, true},
		{"terraform_data.metadata", []string{"input", "input.tier"}, false},
		{"null_resource.deploy", []string{"triggers", "triggers.release"}, true},
		{"null_resource.deploy", []string{"id"}, false},
		{"random_pet.environment", []string{"triggers", "triggers.release"}, false},
		{"aws_instance.web", []string{"tags", `tags["kubernetes.io/role"]`}, true},
	}
	for _, tt := range tests {
		t.Run(tt.address+" "+strings.Join(tt.ancestors, ","), func(t *testing.T) {
			if _, ok := rs.MatchAttribute(tt.address, tt.ancestors); ok != tt.want {
				t.Errorf("MatchAttribute = %v, want %v", ok, tt.want)
			}
		})
	}
}

func TestNilRulesIgnoreNothing(t *testing.T) {
	var rs *Rules
	if _, ok := rs.MatchResource("a.b"); ok {
		t.Error("nil rules matched a resource")
	}
	if _, ok := rs.MatchAttribute("a.b", []string{"x"}); ok {
		t.Error("nil rules matched an attribute")
	}
	if rs.Len() != 0 || rs.All() != nil {
		t.Error("nil rules are not empty")
	}
}

func TestLoad(t *testing.T) {
	rs, err := Load("")
	if err != nil || rs.Len() != 0 {
		t.Fatalf("Load(\"\") = %v, %v", rs, err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".driftignore")
	if err := os.WriteFile(path, []byte("random_pet.*\nbad line here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("Load(invalid) error = %v, want line 2", err)
	}
	if _, err := Load(filepath.Join(dir, "missing")); err == nil {
		t.Error("Load(missing) succeeded")
	}
	if err := os.WriteFile(path, []byte("random_pet.*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rs, err = Load(path)
	if err != nil || rs.Len() != 1 {
		t.Fatalf("Load(valid) = %v, %v", rs, err)
	}
}

func TestRuleString(t *testing.T) {
	if got := (Rule{Address: "a.b"}).String(); got != "a.b" {
		t.Errorf("got %q", got)
	}
	if got := (Rule{Address: "a.b", Attribute: "tags"}).String(); got != "a.b tags" {
		t.Errorf("got %q", got)
	}
}

func TestDottedPattern(t *testing.T) {
	tests := map[string]string{
		"input.owner":                    "input.owner",
		`tags["LastScannedAt"]`:          "tags.LastScannedAt",
		`tags["kubernetes.io/role"]`:     `tags["kubernetes.io/role"]`,
		`tags["*"]`:                      `tags["*"]`,
		`["tags"]["Name"]`:               "tags.Name",
		`a["b"].c["d e"]["f"]`:           `a.b.c["d e"].f`,
		`rules[0]["cidr"]`:               "rules[0].cidr",
		`labels["app-name"]`:             "labels.app-name",
		`broken["unterminated`:           `broken["unterminated`,
		`env["1st"]`:                     `env["1st"]`,
		`metadata[0].labels["team"]`:     "metadata[0].labels.team",
		`*["Name"]`:                      "*.Name",
		`tags["Last*"]`:                  `tags["Last*"]`,
		`tags["a"]["b"]["c"]`:            "tags.a.b.c",
		`x["kubernetes.io/role"]["sub"]`: `x["kubernetes.io/role"].sub`,
	}
	for in, want := range tests {
		if got := dottedPattern(in); got != want {
			t.Errorf("dottedPattern(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestMatchAttributeAcceptsBothKeyStyles feeds MatchAttribute the ancestors
// the drift package produces (dotted and bracket form for every prefix) and
// checks that a map key written in either style matches.
func TestMatchAttributeAcceptsBothKeyStyles(t *testing.T) {
	lastScanned := []string{"tags", "tags.LastScannedAt", `tags["LastScannedAt"]`}
	role := []string{"tags", `tags["kubernetes.io/role"]`}
	nested := []string{"metadata", "metadata[0]", "metadata[0].labels", `metadata[0]["labels"]`,
		"metadata[0].labels.team", `metadata[0]["labels"]["team"]`}
	tests := []struct {
		attribute string
		ancestors []string
		want      bool
	}{
		{`tags["LastScannedAt"]`, lastScanned, true},
		{"tags.LastScannedAt", lastScanned, true},
		{`tags["*"]`, lastScanned, true},
		{"tags.*", lastScanned, true},
		{`tags["Owner"]`, lastScanned, false},
		{`tags["kubernetes.io/role"]`, role, true},
		{`tags["*"]`, role, true},
		{`metadata[0].labels["team"]`, nested, true},
		{`metadata[0]["labels"].team`, nested, true},
		{`metadata[*].labels.team`, nested, true},
		{`metadata[0].labels["owner"]`, nested, false},
	}
	for _, tt := range tests {
		t.Run(tt.attribute, func(t *testing.T) {
			rs := New(Rule{Address: "aws_instance.*", Attribute: tt.attribute})
			if _, ok := rs.MatchAttribute("aws_instance.web", tt.ancestors); ok != tt.want {
				t.Errorf("MatchAttribute(%q) = %v, want %v", tt.attribute, ok, tt.want)
			}
		})
	}
}

func TestUnused(t *testing.T) {
	a := Rule{Address: "random_pet.*", Line: 1}
	b := Rule{Address: "aws_instance.*", Attribute: "tags", Line: 2}
	c := Rule{Address: "aws_instance.*", Attribute: "tags", Line: 3}
	rs := New(a, b, c)
	got := rs.Unused(map[Rule]bool{b: true})
	if len(got) != 2 || got[0] != a || got[1] != c {
		t.Errorf("Unused = %+v, want rules on lines 1 and 3", got)
	}
	var nilRules *Rules
	if nilRules.Unused(nil) != nil {
		t.Error("nil rules reported unused rules")
	}
}
