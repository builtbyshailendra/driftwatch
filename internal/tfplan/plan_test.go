package tfplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadFixture(t *testing.T, name string) *Plan {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "plans", name))
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParseBytes(b)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return p
}

func TestParseRealFixtures(t *testing.T) {
	tests := []struct {
		file           string
		changes        int
		drift          int
		nonNoOpChanges int
	}{
		{file: "clean.json", changes: 12, drift: 0, nonNoOpChanges: 0},
		{file: "update-only.json", changes: 12, drift: 0, nonNoOpChanges: 1},
		{file: "refresh-only.json", changes: 0, drift: 1, nonNoOpChanges: 0},
		{file: "drift.json", changes: 12, drift: 1, nonNoOpChanges: 10},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			p := loadFixture(t, tt.file)
			if p.TerraformVersion != "1.17.1" {
				t.Errorf("terraform_version = %q, want 1.17.1", p.TerraformVersion)
			}
			if got := len(p.ResourceChanges); got != tt.changes {
				t.Errorf("resource_changes = %d, want %d", got, tt.changes)
			}
			if got := len(p.ResourceDrift); got != tt.drift {
				t.Errorf("resource_drift = %d, want %d", got, tt.drift)
			}
			n := 0
			for _, rc := range p.ResourceChanges {
				if strings.Join(rc.Change.Actions, ",") != string(ActionNoOp) {
					n++
				}
			}
			if n != tt.nonNoOpChanges {
				t.Errorf("non no-op changes = %d, want %d", n, tt.nonNoOpChanges)
			}
		})
	}
}

func TestParseKeepsSensitivityMarkersAndReplacePaths(t *testing.T) {
	p := loadFixture(t, "drift.json")
	var found bool
	for _, rc := range p.ResourceChanges {
		if rc.Address != "random_password.database" {
			continue
		}
		found = true
		bs, ok := rc.Change.BeforeSensitive.(map[string]any)
		if !ok || bs["result"] != true {
			t.Errorf("before_sensitive.result not decoded as true: %#v", rc.Change.BeforeSensitive)
		}
		if len(rc.Change.ReplacePaths) != 1 || rc.Change.ReplacePaths[0][0] != "keepers" {
			t.Errorf("replace_paths = %#v", rc.Change.ReplacePaths)
		}
		if got := rc.Change.ActionList(); len(got) != 2 || got[0] != ActionDelete || got[1] != ActionCreate {
			t.Errorf("actions = %v", got)
		}
	}
	if !found {
		t.Fatal("random_password.database missing from fixture")
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "empty"},
		{"not json", "{not json", "decode plan JSON"},
		{"no format version", `{"terraform_version":"1.17.1"}`, "no format_version"},
		{"future major", `{"format_version":"2.0"}`, "unsupported plan format_version"},
		{"errored", `{"format_version":"1.2","errored":true}`, "errored"},
		{"no address", `{"format_version":"1.2","resource_changes":[{"change":{"actions":["create"]}}]}`, "no address"},
		{"no actions", `{"format_version":"1.2","resource_changes":[{"address":"a.b","change":{}}]}`, "no change actions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseBytes([]byte(tt.in))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestParseAcceptsMinorVersions(t *testing.T) {
	for _, v := range []string{"1.0", "1.2", "1.99"} {
		if _, err := ParseBytes([]byte(`{"format_version":"` + v + `"}`)); err != nil {
			t.Errorf("format_version %s: %v", v, err)
		}
	}
}

func TestPathString(t *testing.T) {
	tests := []struct {
		path Path
		want string
	}{
		{nil, ""},
		{Path{KeyStep("id")}, "id"},
		{Path{KeyStep("keepers"), KeyStep("rotation")}, "keepers.rotation"},
		{Path{KeyStep("tags"), KeyStep("kubernetes.io/role")}, `tags["kubernetes.io/role"]`},
		{Path{KeyStep("ingress"), IndexStep(0), KeyStep("cidr_blocks"), IndexStep(2)}, "ingress[0].cidr_blocks[2]"},
		{Path{KeyStep("labels"), KeyStep("app-name")}, "labels.app-name"},
		{Path{KeyStep("env"), KeyStep("1st")}, `env["1st"]`},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.path.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPathFromJSON(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"keys", `["keepers","rotation"]`, "keepers.rotation", false},
		{"index", `["ingress",0,"from_port"]`, "ingress[0].from_port", false},
		{"fraction", `["ingress",1.5]`, "", true},
		{"bool", `["ingress",true]`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dec := json.NewDecoder(strings.NewReader(tt.raw))
			dec.UseNumber()
			var raw []any
			if err := dec.Decode(&raw); err != nil {
				t.Fatal(err)
			}
			p, err := PathFromJSON(raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && p.String() != tt.want {
				t.Errorf("path = %q, want %q", p.String(), tt.want)
			}
		})
	}
}

func TestPathHasPrefixAndAppend(t *testing.T) {
	base := Path{KeyStep("triggers")}
	full := base.Append(KeyStep("release"))
	if len(base) != 1 {
		t.Fatalf("Append mutated the receiver: %v", base)
	}
	tests := []struct {
		p, q Path
		want bool
	}{
		{full, base, true},
		{full, full, true},
		{base, full, false},
		{full, Path{KeyStep("keepers")}, false},
		{full, nil, true},
		{Path{KeyStep("a"), IndexStep(1)}, Path{KeyStep("a"), IndexStep(0)}, false},
	}
	for _, tt := range tests {
		if got := tt.p.HasPrefix(tt.q); got != tt.want {
			t.Errorf("%q.HasPrefix(%q) = %v, want %v", tt.p, tt.q, got, tt.want)
		}
	}
}

func TestIsComplete(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name     string
		complete *bool
		deferred []DeferredChange
		want     bool
	}{
		{"field absent in older plans", nil, nil, true},
		{"complete", &yes, nil, true},
		{"marked incomplete", &no, nil, false},
		{"deferred changes without the flag", nil, []DeferredChange{{Reason: "unknown"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plan{Complete: tt.complete, DeferredChanges: tt.deferred}
			if got := p.IsComplete(); got != tt.want {
				t.Errorf("IsComplete() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseDeferredChanges(t *testing.T) {
	p := loadFixture(t, "deferred.json")
	if p.IsComplete() || p.Complete == nil || *p.Complete {
		t.Errorf("deferred.json should be incomplete: complete=%v", p.Complete)
	}
	if len(p.DeferredChanges) != 2 {
		t.Fatalf("deferred changes = %d, want 2", len(p.DeferredChanges))
	}
	if dc := p.DeferredChanges[0]; dc.Reason != "provider_config_unknown" || dc.ResourceChange.Address != "kubernetes_namespace.apps" {
		t.Errorf("first deferred change = %+v", dc)
	}
	for _, name := range []string{"clean.json", "drift.json"} {
		if !loadFixture(t, name).IsComplete() {
			t.Errorf("%s should be complete", name)
		}
	}
	_, err := ParseBytes([]byte(`{"format_version":"1.2","deferred_changes":[{"reason":"unknown","resource_change":{}}]}`))
	if err == nil || !strings.Contains(err.Error(), "deferred_changes[0]") {
		t.Errorf("deferred change without address: err = %v", err)
	}
}

func TestPathBracketString(t *testing.T) {
	tests := []struct {
		path Path
		want string
	}{
		{nil, ""},
		{Path{KeyStep("id")}, "id"},
		{Path{KeyStep("tags"), KeyStep("LastScannedAt")}, `tags["LastScannedAt"]`},
		{Path{KeyStep("tags"), KeyStep("kubernetes.io/role")}, `tags["kubernetes.io/role"]`},
		{Path{KeyStep("ingress"), IndexStep(0), KeyStep("cidr_blocks")}, `ingress[0]["cidr_blocks"]`},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.path.BracketString(); got != tt.want {
				t.Errorf("BracketString() = %q, want %q", got, tt.want)
			}
		})
	}
	for key, want := range map[string]bool{"Name": true, "app-name": true, "_x": true, "1st": false, "a.b": false, "": false} {
		if got := IsIdentifier(key); got != want {
			t.Errorf("IsIdentifier(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestSensitiveVariableValues(t *testing.T) {
	p := loadFixture(t, "sensitive.json")
	got := p.SensitiveVariableValues()
	if len(got) != 1 || got[0] != "fixture-only-Qv7rTz29mWk" {
		t.Errorf("SensitiveVariableValues = %v", got)
	}
	if v := loadFixture(t, "update-only.json").SensitiveVariableValues(); len(v) != 0 {
		t.Errorf("update-only.json has no sensitive variables, got %v", v)
	}
}
