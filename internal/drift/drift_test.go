package drift

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/builtbyshailendra/driftwatch/internal/ignore"
	"github.com/builtbyshailendra/driftwatch/internal/tfplan"
)

func fixture(t *testing.T, name string) *tfplan.Plan {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "plans", name))
	if err != nil {
		t.Fatal(err)
	}
	p, err := tfplan.ParseBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// decode parses a JSON literal the same way the plan parser does.
func decode(t *testing.T, s string) any {
	t.Helper()
	if s == "" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	return v
}

func TestClassify(t *testing.T) {
	tests := []struct {
		actions []string
		want    Kind
		ok      bool
		wantErr bool
	}{
		{[]string{"no-op"}, "", false, false},
		{[]string{"read"}, "", false, false},
		{[]string{"forget"}, "", false, false},
		{[]string{"create"}, KindCreate, true, false},
		{[]string{"update"}, KindUpdate, true, false},
		{[]string{"delete"}, KindDelete, true, false},
		{[]string{"delete", "create"}, KindReplace, true, false},
		{[]string{"create", "delete"}, KindReplace, true, false},
		{[]string{"teleport"}, "", false, true},
		{nil, "", false, true},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.actions, ","), func(t *testing.T) {
			got, ok, err := Classify(tt.actions)
			if (err != nil) != tt.wantErr || ok != tt.ok || got != tt.want {
				t.Errorf("Classify(%v) = %q, %v, %v", tt.actions, got, ok, err)
			}
		})
	}
}

func TestSeverityAndOrder(t *testing.T) {
	tests := []struct {
		kind Kind
		want Severity
	}{
		{KindDelete, SeverityHigh},
		{KindReplace, SeverityHigh},
		{KindUpdate, SeverityMedium},
		{KindCreate, SeverityLow},
	}
	for i, tt := range tests {
		if got := tt.kind.Severity(); got != tt.want {
			t.Errorf("%s severity = %s, want %s", tt.kind, got, tt.want)
		}
		if tt.kind.rank() != i {
			t.Errorf("%s rank = %d, want %d", tt.kind, tt.kind.rank(), i)
		}
	}
}

func TestDescribeReason(t *testing.T) {
	if got := DescribeReason("delete_because_count_index"); got != "count no longer includes this index" {
		t.Errorf("got %q", got)
	}
	if got := DescribeReason("brand_new_reason"); got != "brand_new_reason" {
		t.Errorf("unknown reasons must pass through, got %q", got)
	}
}

func TestDiffAttributes(t *testing.T) {
	type want struct {
		path                          string
		before, after                 string // JSON of the expected visible value
		bSens, aSens, unknown, forces bool
		contains                      bool // ContainsSensitive
	}
	tests := []struct {
		name                       string
		before, beforeSens         string
		after, afterSens, afterUnk string
		replace                    [][]any
		want                       []want
	}{
		{
			name:   "scalar change",
			before: `{"id":"a","size":1}`, after: `{"id":"a","size":2}`,
			want: []want{{path: "size", before: "1", after: "2"}},
		},
		{
			name:   "nested map key added and removed",
			before: `{"tags":{"Name":"web","Old":"x"}}`, after: `{"tags":{"Name":"web","Team":"sre"}}`,
			want: []want{
				{path: "tags.Old", before: `"x"`, after: "null"},
				{path: "tags.Team", before: "null", after: `"sre"`},
			},
		},
		{
			name:   "list element appended",
			before: `{"ports":[80]}`, after: `{"ports":[80,443]}`,
			want: []want{{path: "ports[1]", before: "null", after: "443"}},
		},
		{
			name:   "null to empty map",
			before: `{"tags":null}`, after: `{"tags":{}}`,
			want: []want{{path: "tags", before: "null", after: "{}"}},
		},
		{
			name:   "unknown attribute absent from after",
			before: `{"id":"abc","result":"x"}`, after: `{}`, afterUnk: `{"id":true,"result":true}`,
			want: []want{
				{path: "id", before: `"abc"`, after: "null", unknown: true},
				{path: "result", before: `"x"`, after: "null", unknown: true},
			},
		},
		{
			name:   "sensitive on both sides hides values",
			before: `{"password":"old-secret"}`, beforeSens: `{"password":true}`,
			after: `{"password":"new-secret"}`, afterSens: `{"password":true}`,
			want: []want{{path: "password", before: "null", after: "null", bSens: true, aSens: true}},
		},
		{
			name:   "unchanged sensitive value is not reported",
			before: `{"password":"same"}`, beforeSens: `{"password":true}`,
			after: `{"password":"same"}`, afterSens: `{"password":true}`,
		},
		{
			name:   "whole subtree sensitive",
			before: `{"conn":{"user":"a","pass":"b"}}`, beforeSens: `{"conn":true}`,
			after: `{"conn":{"user":"a","pass":"c"}}`, afterSens: `{"conn":true}`,
			want: []want{{path: "conn", before: "null", after: "null", bSens: true, aSens: true}},
		},
		{
			name:   "list elements sensitive individually",
			before: `{"keys":["k1","k2"]}`, beforeSens: `{"keys":[false,true]}`,
			after: `{"keys":["k1","k3"]}`, afterSens: `{"keys":[false,true]}`,
			want: []want{{path: "keys[1]", before: "null", after: "null", bSens: true, aSens: true}},
		},
		{
			name:   "replace path marks nested attributes",
			before: `{"keepers":{"rotation":"q1"},"length":10}`, after: `{"keepers":{"rotation":"q2"},"length":12}`,
			replace: [][]any{{"keepers"}},
			want: []want{
				{path: "keepers.rotation", before: `"q1"`, after: `"q2"`, forces: true},
				{path: "length", before: "10", after: "12"},
			},
		},
		{
			name:   "type change from string to object",
			before: `{"v":"text"}`, after: `{"v":{"a":1}}`,
			want: []want{{path: "v", before: `"text"`, after: `{"a":1}`}},
		},
		{
			name:   "partly sensitive object becomes unknown",
			before: `{"input":{"user":"admin","password":"hunter2"}}`, beforeSens: `{"input":{"password":true}}`,
			after: `{}`, afterUnk: `{"input":true}`,
			want: []want{{path: "input", before: `{"password":"(sensitive value)","user":"admin"}`, after: "null", unknown: true, contains: true}},
		},
		{
			name:   "partly sensitive object becomes unknown and sensitive",
			before: `{"input":{"user":"admin","password":"hunter2"}}`, beforeSens: `{"input":{"password":true}}`,
			after: `{}`, afterSens: `{"input":true}`, afterUnk: `{"input":true}`,
			want: []want{{path: "input", before: `{"password":"(sensitive value)","user":"admin"}`, after: "null", aSens: true, unknown: true, contains: true}},
		},
		{
			name:   "partly sensitive object replaced by wholly sensitive value",
			before: `{"input":{"user":"admin","password":"hunter2"}}`, beforeSens: `{"input":{"password":true}}`,
			after: `{"input":"rotated"}`, afterSens: `{"input":true}`,
			want: []want{{path: "input", before: `{"password":"(sensitive value)","user":"admin"}`, after: "null", aSens: true, contains: true}},
		},
		{
			name:   "partly sensitive object replaced by a string",
			before: `{"input":{"user":"admin","password":"hunter2"}}`, beforeSens: `{"input":{"password":true}}`,
			after: `{"input":"admin"}`,
			want:  []want{{path: "input", before: `{"password":"(sensitive value)","user":"admin"}`, after: `"admin"`, contains: true}},
		},
		{
			name:   "string replaced by partly sensitive list",
			before: `{"keys":"none"}`,
			after:  `{"keys":["public","hunter2"]}`, afterSens: `{"keys":[false,true]}`,
			want: []want{{path: "keys", before: `"none"`, after: `["public","(sensitive value)"]`, contains: true}},
		},
		{
			name:   "sensitive object nested in a list that becomes unknown",
			before: `{"rules":[{"name":"a","token":"hunter2"}]}`, beforeSens: `{"rules":[{"token":true}]}`,
			after: `{}`, afterUnk: `{"rules":true}`,
			want: []want{{path: "rules", before: `[{"name":"a","token":"(sensitive value)"}]`, after: "null", unknown: true, contains: true}},
		},
		{
			name:   "marker shape that does not fit the value masks everything",
			before: `{"input":["admin","hunter2"]}`, beforeSens: `{"input":{"password":true}}`,
			after: `{}`, afterUnk: `{"input":true}`,
			want: []want{{path: "input", before: `"(sensitive value)"`, after: "null", unknown: true, contains: true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tfplan.Change{
				Before: decode(t, tt.before), BeforeSensitive: decode(t, tt.beforeSens),
				After: decode(t, tt.after), AfterSensitive: decode(t, tt.afterSens),
				AfterUnknown: decode(t, tt.afterUnk),
			}
			var rps []tfplan.Path
			for _, raw := range tt.replace {
				p, err := tfplan.PathFromJSON(raw)
				if err != nil {
					t.Fatal(err)
				}
				rps = append(rps, p)
			}
			got := DiffAttributes(c, rps, nil)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d attributes, want %d: %+v", len(got), len(tt.want), got)
			}
			for i, w := range tt.want {
				g := got[i]
				gb, _ := json.Marshal(g.Before)
				ga, _ := json.Marshal(g.After)
				if g.Path != w.path || string(gb) != w.before || string(ga) != w.after ||
					g.BeforeSensitive != w.bSens || g.AfterSensitive != w.aSens ||
					g.AfterUnknown != w.unknown || g.ForcesReplacement != w.forces || g.ContainsSensitive != w.contains {
					t.Errorf("attribute %d = {%s %s %s bs=%v as=%v u=%v f=%v cs=%v}, want %+v",
						i, g.Path, gb, ga, g.BeforeSensitive, g.AfterSensitive, g.AfterUnknown, g.ForcesReplacement, g.ContainsSensitive, w)
				}
			}
		})
	}
}

func TestMask(t *testing.T) {
	tests := []struct {
		name, value, marker string
		want                string
		masked              bool
	}{
		{"no marker", `{"a":1}`, ``, `{"a":1}`, false},
		{"false marker", `"x"`, `false`, `"x"`, false},
		{"empty object marker", `{"a":1}`, `{}`, `{"a":1}`, false},
		{"marker with only false leaves", `{"a":1}`, `{"a":false}`, `{"a":1}`, false},
		{"whole value", `{"a":1}`, `true`, `"(sensitive value)"`, true},
		{"one key", `{"a":1,"b":2}`, `{"b":true}`, `{"a":1,"b":"(sensitive value)"}`, true},
		{"nested key", `{"a":{"b":{"c":"s"}}}`, `{"a":{"b":{"c":true}}}`, `{"a":{"b":{"c":"(sensitive value)"}}}`, true},
		{"list element", `["a","b","c"]`, `[false,true]`, `["a","(sensitive value)","c"]`, true},
		{"object marker on string", `"secret"`, `{"k":true}`, `"(sensitive value)"`, true},
		{"list marker on object", `{"k":"secret"}`, `[true]`, `"(sensitive value)"`, true},
		{"unexpected marker type", `"secret"`, `"yes"`, `"(sensitive value)"`, true},
		{"null value", ``, `true`, `null`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, masked := Mask(decode(t, tt.value), decode(t, tt.marker))
			b, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tt.want || masked != tt.masked {
				t.Errorf("Mask = %s, %v; want %s, %v", b, masked, tt.want, tt.masked)
			}
		})
	}
}

// sensitiveLeaves collects every scalar value that a sensitivity marker
// covers, so tests can search the output for it. Values shorter than six
// characters ("admin", "24") are skipped: they occur in unrelated places and
// are not what the test is looking for.
func sensitiveLeaves(v, marker any, covered bool, out *[]string) {
	covered = covered || isTrue(marker)
	switch val := v.(type) {
	case map[string]any:
		for k, el := range val {
			sensitiveLeaves(el, child(marker, k), covered, out)
		}
	case []any:
		for i, el := range val {
			sensitiveLeaves(el, childIndex(marker, i), covered, out)
		}
	case nil:
	default:
		if s := strings.TrimSpace(fmt.Sprint(val)); covered && len(s) >= 6 {
			*out = append(*out, s)
		}
	}
}

// TestNoFixtureLeaksAnyMarkedValue checks every committed plan fixture: no
// value that the plan marks sensitive, before or after, may appear anywhere in
// the analysis result.
func TestNoFixtureLeaksAnyMarkedValue(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "plans", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	total := 0
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			p := fixture(t, filepath.Base(f))
			var secrets []string
			all := append(append([]tfplan.ResourceChange(nil), p.ResourceChanges...), p.ResourceDrift...)
			for _, rc := range all {
				sensitiveLeaves(rc.Change.Before, rc.Change.BeforeSensitive, false, &secrets)
				sensitiveLeaves(rc.Change.After, rc.Change.AfterSensitive, false, &secrets)
			}
			total += len(secrets)
			res, err := Analyze(p, nil)
			if err != nil {
				t.Fatal(err)
			}
			out, err := json.Marshal(res)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range secrets {
				if strings.Contains(string(out), s) {
					t.Errorf("sensitive value leaked into result: %.8s...", s)
				}
			}
		})
	}
	if total == 0 {
		t.Error("fixtures contain no sensitive values; the test proves nothing")
	}
}

// TestSensitiveFixtureKeepsVisibleParts uses a real Terraform plan in which an
// object with one sensitive key is replaced by an unknown value and by a
// string, and in which terraform_data copies that object to "output" without
// marks. Every old value must stay readable apart from the secret key.
func TestSensitiveFixtureKeepsVisibleParts(t *testing.T) {
	res, err := Analyze(fixture(t, "sensitive.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{ // address path -> forces replacement
		"terraform_data.echo input":               false,
		"terraform_data.echo output":              false,
		"terraform_data.retype triggers_replace":  true,
		"terraform_data.unknown triggers_replace": true,
	}
	for _, c := range res.Changes {
		for _, a := range c.Attributes {
			key := c.Address + " " + a.Path
			forces, ok := want[key]
			if !ok {
				continue
			}
			delete(want, key)
			before, isMap := a.Before.(map[string]any)
			if !isMap || before["user"] != "admin" || before["password"] != SensitivePlaceholder {
				t.Errorf("%s: before = %#v", key, a.Before)
			}
			if !a.ContainsSensitive || !a.Sensitive() || a.ForcesReplacement != forces {
				t.Errorf("%s: flags = %+v", key, a)
			}
		}
	}
	if len(want) > 0 {
		t.Errorf("attributes not reported: %v", want)
	}
}

func TestSecretsScrub(t *testing.T) {
	plan := &tfplan.Plan{ResourceChanges: []tfplan.ResourceChange{{
		Address: "a.b",
		Change: tfplan.Change{
			Actions:         []string{"update"},
			Before:          decode(t, `{"password":"hunter2-long","pin":"4711","port":5432,"name":"db"}`),
			BeforeSensitive: decode(t, `{"password":true,"pin":true,"port":true}`),
			After:           decode(t, `{"body":"line\n"}`),
			AfterSensitive:  decode(t, `{"body":true}`),
		},
	}}}
	s := CollectSecrets(plan)
	if s.Len() != 4 { // hunter2-long, 4711, "line\n" and its trimmed form "line"
		t.Errorf("Len = %d, want 4", s.Len())
	}
	tests := []struct {
		name, in, want string
		changed        bool
	}{
		{"exact match", `"hunter2-long"`, `"(sensitive value)"`, true},
		{"short exact match", `"4711"`, `"(sensitive value)"`, true},
		{"trimmed match", `"line"`, `"(sensitive value)"`, true},
		{"embedded long secret", `"postgres://app:hunter2-long@db"`, `"postgres://app:(sensitive value)@db"`, true},
		{"short secret is not searched inside strings", `"room 4711"`, `"room 4711"`, false},
		{"numbers are left alone", `5432`, `5432`, false},
		{"unmarked value", `"db"`, `"db"`, false},
		{"nested", `{"conn":["x","hunter2-long"],"n":1}`, `{"conn":["x","(sensitive value)"],"n":1}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := s.Scrub(decode(t, tt.in))
			b, _ := json.Marshal(got)
			if string(b) != tt.want || changed != tt.changed {
				t.Errorf("Scrub(%s) = %s, %v; want %s, %v", tt.in, b, changed, tt.want, tt.changed)
			}
		})
	}
	var none *Secrets
	if v, changed := none.Scrub("x"); v != "x" || changed {
		t.Error("nil Secrets changed a value")
	}
}

// TestUnmarkedCopiesOfSecretsAreMasked covers values Terraform does not mark:
// a copy of a marked value in another attribute, and the value of a root
// variable declared sensitive.
func TestUnmarkedCopiesOfSecretsAreMasked(t *testing.T) {
	plan := &tfplan.Plan{
		Variables: map[string]tfplan.VariableValue{"db_password": {Value: "from-a-variable-9z"}, "region": {Value: "eu-west-1"}},
		ResourceChanges: []tfplan.ResourceChange{{
			Address: "terraform_data.creds", Mode: "managed",
			Change: tfplan.Change{
				Actions:         []string{"update"},
				Before:          decode(t, `{"input":{"password":"hunter2-long"},"output":{"password":"hunter2-long"},"note":"from-a-variable-9z","region":"eu-west-1"}`),
				BeforeSensitive: decode(t, `{"input":{"password":true},"output":{}}`),
				After:           decode(t, `{"note":"rotated","region":"eu-west-2"}`),
				AfterSensitive:  decode(t, `{"input":true}`),
				AfterUnknown:    decode(t, `{"input":true,"output":true}`),
			},
		}},
	}
	plan.Configuration.RootModule.Variables = map[string]tfplan.VariableDeclaration{
		"db_password": {Sensitive: true}, "region": {},
	}
	res, err := Analyze(plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"hunter2-long", "from-a-variable-9z"} {
		if strings.Contains(string(out), secret) {
			t.Errorf("result contains %q: %s", secret, out)
		}
	}
	if !strings.Contains(string(out), "eu-west-1") {
		t.Error("a variable that is not sensitive was masked")
	}
	for _, a := range res.Changes[0].Attributes {
		if a.Path == "output" && !a.ContainsSensitive {
			t.Error("output was scrubbed but not flagged")
		}
	}
}

func TestAnalyzeFixtures(t *testing.T) {
	tests := []struct {
		file                               string
		hasDrift                           bool
		del, replace, update, create, outs int
	}{
		{"clean.json", false, 0, 0, 0, 0, 0},
		{"update-only.json", true, 0, 0, 1, 0, 0},
		{"refresh-only.json", true, 0, 0, 0, 0, 1},
		{"drift.json", true, 3, 5, 1, 1, 1},
		{"sensitive.json", true, 0, 2, 1, 0, 0},
		{"deferred.json", true, 0, 0, 1, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			res, err := Analyze(fixture(t, tt.file), nil)
			if err != nil {
				t.Fatal(err)
			}
			if res.HasDrift() != tt.hasDrift {
				t.Errorf("HasDrift = %v, want %v", res.HasDrift(), tt.hasDrift)
			}
			c := res.Counts()
			if c[KindDelete] != tt.del || c[KindReplace] != tt.replace || c[KindUpdate] != tt.update || c[KindCreate] != tt.create {
				t.Errorf("counts = %v", c)
			}
			if len(res.Outside) != tt.outs {
				t.Errorf("outside = %d, want %d", len(res.Outside), tt.outs)
			}
			if res.TerraformVersion != "1.17.1" {
				t.Errorf("terraform version = %q", res.TerraformVersion)
			}
		})
	}
}

func TestAnalyzeDriftOrderingAndDetails(t *testing.T) {
	res, err := Analyze(fixture(t, "drift.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range res.Changes {
		got = append(got, string(c.Kind)+" "+c.Address)
	}
	want := []string{
		`delete module.service["worker"].null_resource.rollout`,
		`delete module.service["worker"].random_integer.port`,
		`delete null_resource.cache_warmer[0]`,
		`replace local_sensitive_file.database_env`,
		`replace module.service["api"].null_resource.rollout`,
		`replace null_resource.deploy`,
		`replace random_password.database`,
		`replace random_pet.environment`,
		`update terraform_data.metadata`,
		`create local_file.inventory`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("changes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	byAddr := map[string]Change{}
	for _, c := range res.Changes {
		byAddr[c.Address] = c
	}
	worker := byAddr[`module.service["worker"].null_resource.rollout`]
	if worker.ModuleAddress != `module.service["worker"]` || worker.Reason != "its module call was removed from configuration" {
		t.Errorf("worker change = %+v", worker)
	}
	meta := byAddr["terraform_data.metadata"]
	if len(meta.Attributes) == 0 || meta.Attributes[0].Path != "input.owner" ||
		meta.Attributes[0].Before != "platform-team" || meta.Attributes[0].After != "data-platform" {
		t.Errorf("metadata attributes = %+v", meta.Attributes)
	}
	if out := res.Outside[0]; out.Address != "local_file.inventory" || out.Kind != KindDelete || out.Source != SourceOutside {
		t.Errorf("outside change = %+v", out)
	}
}

// TestAnalyzeNeverLeaksSensitiveValues takes the real secrets out of the
// fixture and checks that none of them survive into the analysis result.
func TestAnalyzeNeverLeaksSensitiveValues(t *testing.T) {
	p := fixture(t, "drift.json")
	var secrets []string
	for _, rc := range p.ResourceChanges {
		before, _ := rc.Change.Before.(map[string]any)
		switch rc.Address {
		case "random_password.database":
			secrets = append(secrets, before["result"].(string), before["bcrypt_hash"].(string))
		case "local_sensitive_file.database_env":
			secrets = append(secrets, strings.TrimSpace(before["content"].(string)))
		}
	}
	if len(secrets) != 3 {
		t.Fatalf("expected 3 secrets in fixture, found %d", len(secrets))
	}
	res, err := Analyze(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range secrets {
		if strings.Contains(string(out), s) || strings.Contains(string(out), strings.TrimPrefix(s, "DATABASE_PASSWORD=")) {
			t.Errorf("sensitive value leaked into result: %.8s...", s)
		}
	}
	for _, c := range res.Changes {
		for _, a := range c.Attributes {
			if a.BeforeSensitive && a.Before != nil || a.AfterSensitive && a.After != nil {
				t.Errorf("%s %s keeps a value on a sensitive side", c.Address, a.Path)
			}
		}
	}
}

func TestAnalyzeWithIgnoreRules(t *testing.T) {
	tests := []struct {
		name        string
		file        string
		rules       []ignore.Rule
		wantChanges int
		wantOutside int
		wantIgnored []string
	}{
		{
			name:        "attribute rule suppresses the only update",
			file:        "update-only.json",
			rules:       []ignore.Rule{{Address: "terraform_data.metadata", Attribute: "input.owner"}},
			wantIgnored: []string{"terraform_data.metadata"},
		},
		{
			name:        "parent attribute rule covers children",
			file:        "update-only.json",
			rules:       []ignore.Rule{{Address: "terraform_data.*", Attribute: "input"}},
			wantIgnored: []string{"terraform_data.metadata"},
		},
		{
			name:        "rule for another attribute keeps the change",
			file:        "update-only.json",
			rules:       []ignore.Rule{{Address: "terraform_data.metadata", Attribute: "input.tier"}},
			wantChanges: 1,
		},
		{
			name:        "resource rule applies to outside changes",
			file:        "refresh-only.json",
			rules:       []ignore.Rule{{Address: "local_file.*"}},
			wantIgnored: []string{"local_file.inventory"},
		},
		{
			name: "module glob and replacement cause",
			file: "drift.json",
			rules: []ignore.Rule{
				{Address: `module.service["worker"].*`},
				{Address: "random_password.database", Attribute: "keepers"},
			},
			wantChanges: 7,
			wantOutside: 1,
			wantIgnored: []string{
				`module.service["worker"].null_resource.rollout`,
				`module.service["worker"].random_integer.port`,
				"random_password.database",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Analyze(fixture(t, tt.file), ignore.New(tt.rules...))
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Changes) != tt.wantChanges || len(res.Outside) != tt.wantOutside {
				t.Errorf("changes = %d outside = %d, want %d and %d", len(res.Changes), len(res.Outside), tt.wantChanges, tt.wantOutside)
			}
			var ignored []string
			for _, ig := range res.Ignored {
				ignored = append(ignored, ig.Address)
				if len(ig.Rules) == 0 {
					t.Errorf("%s ignored without naming a rule", ig.Address)
				}
			}
			if strings.Join(ignored, ",") != strings.Join(tt.wantIgnored, ",") {
				t.Errorf("ignored = %v, want %v", ignored, tt.wantIgnored)
			}
		})
	}
}

func TestPartiallyIgnoredChangeKeepsRemainingAttributes(t *testing.T) {
	p := &tfplan.Plan{FormatVersion: "1.2", ResourceChanges: []tfplan.ResourceChange{{
		Address: "null_resource.deploy", Mode: "managed", Type: "null_resource", Name: "deploy",
		Change: tfplan.Change{
			Actions: []string{"update"},
			Before:  decode(t, `{"triggers":{"release":"1","region":"eu-west-1"}}`),
			After:   decode(t, `{"triggers":{"release":"2","region":"eu-central-1"}}`),
		},
	}}}
	rules := ignore.New(ignore.Rule{Address: "null_resource.deploy", Attribute: "triggers.release"})
	res, err := Analyze(p, rules)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 1 {
		t.Fatalf("changes = %+v, want the update to survive", res.Changes)
	}
	c := res.Changes[0]
	if len(c.IgnoredAttributes) != 1 || c.IgnoredAttributes[0].Path != "triggers.release" {
		t.Errorf("ignored attributes = %+v", c.IgnoredAttributes)
	}
	if len(c.Attributes) != 1 || c.Attributes[0].Path != "triggers.region" {
		t.Errorf("remaining attributes = %+v", c.Attributes)
	}
}

// Attributes that only become unknown are consequences, so ignoring the one
// known cause of a replacement suppresses the whole change.
func TestUnknownAttributesDoNotKeepIgnoredChangeAlive(t *testing.T) {
	rules := ignore.New(ignore.Rule{Address: `module.service["api"].null_resource.rollout`, Attribute: "triggers.release"})
	res, err := Analyze(fixture(t, "drift.json"), rules)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Changes {
		if c.Address == `module.service["api"].null_resource.rollout` {
			t.Fatalf("change should be ignored, got %+v", c)
		}
	}
	if len(res.Ignored) != 1 || res.Ignored[0].Kind != KindReplace {
		t.Errorf("ignored = %+v", res.Ignored)
	}
}

func TestAnalyzeSkipsDataSourcesAndRejectsUnknownActions(t *testing.T) {
	p := &tfplan.Plan{FormatVersion: "1.2", ResourceChanges: []tfplan.ResourceChange{
		{Address: "data.http.ip", Mode: "data", Change: tfplan.Change{Actions: []string{"read"}}},
		{Address: "data.http.weird", Mode: "data", Change: tfplan.Change{Actions: []string{"create"}}},
	}}
	res, err := Analyze(p, nil)
	if err != nil || res.HasDrift() {
		t.Fatalf("data sources: res=%+v err=%v", res, err)
	}
	p.ResourceChanges = append(p.ResourceChanges, tfplan.ResourceChange{
		Address: "null_resource.x", Mode: "managed", Change: tfplan.Change{Actions: []string{"explode"}},
	})
	if _, err := Analyze(p, nil); err == nil || !strings.Contains(err.Error(), "null_resource.x") {
		t.Errorf("unknown action error = %v", err)
	}
}

// lastScannedPlan is an aws_instance whose only drift is a tag that a
// scanner rewrites, plus an unrelated tag with a key that is not an
// identifier.
func lastScannedPlan(t *testing.T, extraTag bool) *tfplan.Plan {
	t.Helper()
	after := `{"id":"i-0abc","tags":{"LastScannedAt":"2026-11-08T02:00:00Z","Name":"web"}}`
	if extraTag {
		after = `{"id":"i-0abc","tags":{"LastScannedAt":"2026-11-08T02:00:00Z","Name":"web","kubernetes.io/role":"node"}}`
	}
	return &tfplan.Plan{
		TerraformVersion: "1.17.1",
		ResourceChanges: []tfplan.ResourceChange{{
			Address: "aws_instance.web", Mode: "managed", Type: "aws_instance", Name: "web",
			Change: tfplan.Change{
				Actions:         []string{"update"},
				Before:          decode(t, `{"id":"i-0abc","tags":{"LastScannedAt":"2026-11-01T02:00:00Z","Name":"web"}}`),
				After:           decode(t, after),
				BeforeSensitive: decode(t, `{}`), AfterSensitive: decode(t, `{}`), AfterUnknown: decode(t, `{}`),
			},
		}},
	}
}

// TestIgnoreRuleKeyStyles covers the README example: a rule written with a
// bracketed map key must match even though reports render the key dotted.
func TestIgnoreRuleKeyStyles(t *testing.T) {
	tests := []struct {
		name        string
		attribute   string
		extraTag    bool
		wantIgnored bool
		wantKept    []string
	}{
		{"bracket form from the README", `tags["LastScannedAt"]`, false, true, nil},
		{"dotted form", "tags.LastScannedAt", false, true, nil},
		{"bracket glob", `tags["Last*"]`, false, true, nil},
		{"other keys keep the change alive", `tags["LastScannedAt"]`, true, false, []string{`tags["kubernetes.io/role"]`}},
		{"non-identifier key in brackets", `tags["kubernetes.io/role"]`, true, false, []string{"tags.LastScannedAt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules := ignore.New(ignore.Rule{Address: "aws_instance.*", Attribute: tt.attribute, Line: 1})
			res, err := Analyze(lastScannedPlan(t, tt.extraTag), rules)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(res.Ignored) == 1; got != tt.wantIgnored {
				t.Fatalf("ignored = %+v, want ignored %v", res.Ignored, tt.wantIgnored)
			}
			if len(res.UnusedRules) != 0 {
				t.Errorf("rule reported unused: %+v", res.UnusedRules)
			}
			if tt.wantIgnored {
				return
			}
			var kept []string
			for _, a := range res.Changes[0].Attributes {
				kept = append(kept, a.Path)
			}
			if strings.Join(kept, ",") != strings.Join(tt.wantKept, ",") {
				t.Errorf("kept attributes = %v, want %v", kept, tt.wantKept)
			}
		})
	}
}

func TestAnalyzeReportsUnusedRules(t *testing.T) {
	used := ignore.Rule{Address: "terraform_data.metadata", Attribute: "input.owner", Line: 1}
	typo := ignore.Rule{Address: "terraform_data.metadata", Attribute: `input["ownr"]`, Line: 2}
	other := ignore.Rule{Address: "aws_instance.*", Line: 3}
	res, err := Analyze(fixture(t, "update-only.json"), ignore.New(used, typo, other))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.UnusedRules) != 2 || res.UnusedRules[0] != typo || res.UnusedRules[1] != other {
		t.Errorf("unused rules = %+v, want lines 2 and 3", res.UnusedRules)
	}
}

func TestAnalyzeDeferredPlan(t *testing.T) {
	res, err := Analyze(fixture(t, "deferred.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Incomplete {
		t.Error("deferred plan not flagged incomplete")
	}
	want := []Deferred{
		{Address: "kubernetes_namespace.apps", Actions: []string{"update"}, Reason: "its provider configuration is known only after apply"},
		{Address: "terraform_data.node[*]", Actions: []string{"create"}, Reason: "count or for_each is known only after apply"},
	}
	if len(res.Deferred) != len(want) {
		t.Fatalf("deferred = %+v", res.Deferred)
	}
	for i, w := range want {
		g := res.Deferred[i]
		if g.Address != w.Address || strings.Join(g.Actions, ",") != strings.Join(w.Actions, ",") || g.Reason != w.Reason {
			t.Errorf("deferred[%d] = %+v, want %+v", i, g, w)
		}
	}
	complete, err := Analyze(fixture(t, "drift.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if complete.Incomplete || len(complete.Deferred) != 0 {
		t.Errorf("complete plan flagged incomplete: %+v", complete.Deferred)
	}
	if got := DescribeDeferredReason("new_reason"); got != "new_reason" {
		t.Errorf("unknown reason = %q", got)
	}
}

func TestAddressesCountsEachResourceOnce(t *testing.T) {
	res, err := Analyze(fixture(t, "drift.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	// local_file.inventory was deleted outside Terraform and the plan
	// recreates it: two entries, one resource.
	if n := len(res.Changes) + len(res.Outside); n != 11 {
		t.Fatalf("entries = %d, want 11", n)
	}
	if got := res.Addresses(); got != 10 {
		t.Errorf("Addresses() = %d, want 10", got)
	}
}
