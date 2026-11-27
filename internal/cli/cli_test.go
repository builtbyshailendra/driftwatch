package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/builtbyshailendra/driftwatch/internal/terraform"
)

var plansDir = filepath.Join("..", "..", "testdata", "plans")

func plan(name string) string { return filepath.Join(plansDir, name) }

type harness struct {
	app    *App
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	env    map[string]string
}

func newHarness() *harness {
	h := &harness{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, env: map[string]string{}}
	h.app = &App{
		Stdin:    strings.NewReader(""),
		Stdout:   h.stdout,
		Stderr:   h.stderr,
		Getenv:   func(k string) string { return h.env[k] },
		HTTP:     &http.Client{Timeout: 5 * time.Second},
		Executor: failingExecutor{},
		Now:      func() time.Time { return time.Date(2026, 11, 7, 6, 0, 0, 0, time.UTC) },
		Version:  "1.2.3",
	}
	return h
}

func (h *harness) run(args ...string) int {
	return h.app.Run(context.Background(), args)
}

// failingExecutor makes sure tests that should not touch terraform do not.
type failingExecutor struct{}

func (failingExecutor) Run(context.Context, string, string, ...string) (terraform.Output, error) {
	panic("terraform must not be executed in this test")
}

func TestCheckPlanJSON(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantCode     int
		wantStdout   []string
		wantStderr   string
		stdoutAbsent bool
	}{
		{
			name:       "markdown with drift",
			args:       []string{"--plan-json", plan("drift.json"), "--workspace", "prod"},
			wantCode:   ExitOK,
			wantStdout: []string{"# Terraform drift report: prod", "`random_password.database` replace"},
			wantStderr: "prod: 3 to delete, 5 to replace, 1 to update, 1 to create, 1 changed outside Terraform",
		},
		{
			name:       "exit code on drift",
			args:       []string{"--plan-json", plan("update-only.json"), "--exit-code"},
			wantCode:   ExitDrift,
			wantStdout: []string{"`terraform_data.metadata` update"},
		},
		{
			name:       "clean plan with exit code",
			args:       []string{"--plan-json", plan("clean.json"), "--exit-code", "--format", "json"},
			wantCode:   ExitOK,
			wantStdout: []string{`"has_drift": false`},
			wantStderr: "no drift",
		},
		{
			name:         "format none",
			args:         []string{"--plan-json", plan("drift.json"), "--format", "none"},
			wantCode:     ExitOK,
			stdoutAbsent: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness()
			code := h.run(append([]string{"check"}, tt.args...)...)
			if code != tt.wantCode {
				t.Fatalf("exit = %d, want %d; stderr: %s", code, tt.wantCode, h.stderr)
			}
			for _, s := range tt.wantStdout {
				if !strings.Contains(h.stdout.String(), s) {
					t.Errorf("stdout lacks %q", s)
				}
			}
			if tt.stdoutAbsent && h.stdout.Len() != 0 {
				t.Errorf("stdout should be empty, got %d bytes", h.stdout.Len())
			}
			if !strings.Contains(h.stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want %q", h.stderr, tt.wantStderr)
			}
		})
	}
}

func TestCheckReadsStdinAndWritesReportFiles(t *testing.T) {
	b, err := os.ReadFile(plan("refresh-only.json"))
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness()
	h.app.Stdin = bytes.NewReader(b)
	dir := t.TempDir()
	md := filepath.Join(dir, "reports", "drift.md")
	js := filepath.Join(dir, "reports", "drift.json")
	if code := h.run("check", "--plan-json", "-", "--workspace", "edge", "--format", "none", "--markdown-out", md, "--json-out", js); code != ExitOK {
		t.Fatalf("exit = %d: %s", code, h.stderr)
	}
	mdBytes, err := os.ReadFile(md)
	if err != nil || !strings.Contains(string(mdBytes), "## Changed outside Terraform") {
		t.Errorf("markdown file = %q, %v", mdBytes, err)
	}
	var rep struct {
		Workspace string `json:"workspace"`
		Summary   struct {
			Outside int `json:"outside"`
		} `json:"summary"`
	}
	jsBytes, err := os.ReadFile(js)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(jsBytes, &rep); err != nil || rep.Workspace != "edge" || rep.Summary.Outside != 1 {
		t.Errorf("json report = %+v, %v", rep, err)
	}
}

func TestCheckIgnoreFile(t *testing.T) {
	dir := t.TempDir()
	rules := "# owner churn is expected\nterraform_data.metadata input.owner\n"

	explicit := filepath.Join(dir, "rules.txt")
	if err := os.WriteFile(explicit, []byte(rules), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness()
	if code := h.run("check", "--plan-json", plan("update-only.json"), "--ignore-file", explicit, "--exit-code"); code != ExitOK {
		t.Errorf("explicit ignore file: exit = %d: %s", code, h.stderr)
	}

	// .driftignore in --dir is picked up automatically.
	if err := os.WriteFile(filepath.Join(dir, DefaultIgnoreFile), []byte(rules), 0o600); err != nil {
		t.Fatal(err)
	}
	h = newHarness()
	if code := h.run("check", "--dir", dir, "--plan-json", plan("update-only.json"), "--exit-code"); code != ExitOK {
		t.Errorf("default ignore file: exit = %d: %s", code, h.stderr)
	}
	if !strings.Contains(h.stdout.String(), "## Ignored by rules") {
		t.Error("report does not list ignored changes")
	}
	if !strings.Contains(h.stderr.String(), filepath.Base(dir)+": no drift") {
		t.Errorf("workspace should default to the directory name: %s", h.stderr)
	}
}

func TestCheckErrors(t *testing.T) {
	dir := t.TempDir()
	badRules := filepath.Join(dir, "bad")
	if err := os.WriteFile(badRules, []byte("a b c\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	notJSON := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(notJSON, []byte("Terraform will perform the following actions"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no command", nil, "Usage:"},
		{"unknown command", []string{"plan"}, `unknown command "plan"`},
		{"missing plan file", []string{"check", "--plan-json", filepath.Join(dir, "nope.json")}, "read plan JSON"},
		{"text instead of JSON", []string{"check", "--plan-json", notJSON}, "decode plan JSON"},
		{"both plan sources", []string{"check", "--plan-json", "a", "--plan-file", "b"}, "mutually exclusive"},
		{"bad format", []string{"check", "--format", "yaml"}, "--format must be"},
		{"stray argument", []string{"check", "extra"}, "unexpected arguments"},
		{"bad rules", []string{"check", "--plan-json", plan("clean.json"), "--ignore-file", badRules}, "line 1"},
		{"unknown flag", []string{"check", "--colour"}, "flag provided but not defined"},
		{"github without token", []string{"check", "--plan-json", plan("drift.json"), "--format", "none", "--github-issue", "--github-repo", "acme/infra"}, "GITHUB_TOKEN"},
		{"github bad repo", []string{"check", "--plan-json", plan("drift.json"), "--format", "none", "--github-issue", "--github-repo", "acme"}, "owner/name"},
		{"slack without webhook", []string{"check", "--plan-json", plan("drift.json"), "--format", "none", "--slack"}, "SLACK_WEBHOOK_URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness()
			if tt.name == "github bad repo" {
				h.env["GITHUB_TOKEN"] = "t"
			}
			if code := h.run(tt.args...); code != ExitError {
				t.Errorf("exit = %d, want %d", code, ExitError)
			}
			if !strings.Contains(h.stderr.String(), tt.want) {
				t.Errorf("stderr = %q, want %q", h.stderr, tt.want)
			}
		})
	}
}

// recorder is a combined fake for the GitHub API and a Slack webhook.
type recorder struct {
	mu       sync.Mutex
	requests []string
	bodies   map[string]string
	issues   string // JSON returned by the issue search
}

func (rec *recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	key := r.Method + " " + r.URL.Path
	rec.requests = append(rec.requests, key)
	rec.bodies[key] = string(body)
	switch {
	case key == "POST /slack/hook":
		_, _ = io.WriteString(w, "ok")
	case key == "GET /repos/acme/infra/issues":
		if r.Header.Get("Authorization") != "Bearer gh-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, rec.issues)
	case key == "POST /repos/acme/infra/issues":
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"number":12,"html_url":"https://github.com/acme/infra/issues/12","state":"open"}`)
	case strings.HasPrefix(key, "POST /repos/acme/infra/issues/5/comments"):
		_, _ = io.WriteString(w, `{}`)
	case key == "PATCH /repos/acme/infra/issues/5":
		_, _ = io.WriteString(w, `{"number":5,"html_url":"https://github.com/acme/infra/issues/5","state":"closed"}`)
	default:
		http.NotFound(w, r)
	}
}

func TestCheckNotifiesGitHubAndSlack(t *testing.T) {
	tests := []struct {
		name         string
		plan         string
		issues       string
		extra        []string
		wantRequests []string
		wantStderr   string
	}{
		{
			name:   "drift opens issue and posts with link",
			plan:   "drift.json",
			issues: `[]`,
			wantRequests: []string{
				"GET /repos/acme/infra/issues",
				"POST /repos/acme/infra/issues",
				"POST /slack/hook",
			},
			wantStderr: "issue #12 created",
		},
		{
			name:   "clean plan closes the issue and stays quiet in Slack",
			plan:   "clean.json",
			issues: `[{"number":5,"state":"open","body":"old\n<!-- driftwatch:workspace=prod -->"}]`,
			wantRequests: []string{
				"GET /repos/acme/infra/issues",
				"POST /repos/acme/infra/issues/5/comments",
				"PATCH /repos/acme/infra/issues/5",
			},
			wantStderr: "issue #5 closed",
		},
		{
			name:         "slack-on-clean posts for clean plans",
			plan:         "clean.json",
			issues:       `[]`,
			extra:        []string{"--slack-on-clean"},
			wantRequests: []string{"GET /repos/acme/infra/issues", "POST /slack/hook"},
			wantStderr:   "posted summary to Slack",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{bodies: map[string]string{}, issues: tt.issues}
			srv := httptest.NewServer(rec)
			defer srv.Close()
			h := newHarness()
			h.env["GITHUB_TOKEN"] = "gh-test"
			h.env["GITHUB_API_URL"] = srv.URL
			h.env["GITHUB_REPOSITORY"] = "acme/infra"
			h.env["SLACK_WEBHOOK_URL"] = srv.URL + "/slack/hook"
			args := append([]string{"check", "--plan-json", plan(tt.plan), "--workspace", "prod", "--format", "none", "--github-issue", "--slack"}, tt.extra...)
			if code := h.run(args...); code != ExitOK {
				t.Fatalf("exit = %d: %s", code, h.stderr)
			}
			if strings.Join(rec.requests, "\n") != strings.Join(tt.wantRequests, "\n") {
				t.Errorf("requests:\n%s\nwant:\n%s", strings.Join(rec.requests, "\n"), strings.Join(tt.wantRequests, "\n"))
			}
			if !strings.Contains(h.stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want %q", h.stderr, tt.wantStderr)
			}
			if body, ok := rec.bodies["POST /repos/acme/infra/issues"]; ok {
				if !strings.Contains(body, "driftwatch:workspace=prod") || !strings.Contains(body, `"labels":["driftwatch","terraform-drift"]`) {
					t.Errorf("issue payload = %.300s", body)
				}
			}
			if body, ok := rec.bodies["POST /slack/hook"]; ok && tt.plan == "drift.json" {
				if !strings.Contains(body, "issues/12|tracking issue") {
					t.Errorf("slack payload lacks issue link: %.400s", body)
				}
			}
		})
	}
}

func TestCheckFailsWhenNotificationFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "internal_error")
	}))
	defer srv.Close()
	h := newHarness()
	h.env["SLACK_WEBHOOK_URL"] = srv.URL
	if code := h.run("check", "--plan-json", plan("drift.json"), "--slack", "--exit-code"); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(h.stdout.String(), "# Terraform drift report") {
		t.Error("report should still be printed when a notification fails")
	}
}

type scriptedExecutor struct {
	planJSON []byte
	calls    []string
}

func (s *scriptedExecutor) Run(_ context.Context, dir, name string, args ...string) (terraform.Output, error) {
	s.calls = append(s.calls, dir+": "+name+" "+strings.Join(args, " "))
	if args[0] == "plan" {
		return terraform.Output{ExitCode: terraform.ExitChanges}, nil
	}
	return terraform.Output{Stdout: s.planJSON}, nil
}

func TestCheckRunsTerraform(t *testing.T) {
	b, err := os.ReadFile(plan("drift.json"))
	if err != nil {
		t.Fatal(err)
	}
	exec := &scriptedExecutor{planJSON: b}
	h := newHarness()
	h.app.Executor = exec
	h.env["TERRAFORM"] = "/opt/terraform"
	code := h.run("check", "--dir", "infra/prod", "--plan-arg=-var-file=prod.tfvars", "--plan-arg=-refresh=true", "--format", "none", "--exit-code")
	if code != ExitDrift {
		t.Fatalf("exit = %d: %s", code, h.stderr)
	}
	if len(exec.calls) != 2 {
		t.Fatalf("calls = %v", exec.calls)
	}
	if !strings.HasPrefix(exec.calls[0], "infra/prod: /opt/terraform plan ") ||
		!strings.HasSuffix(exec.calls[0], "-var-file=prod.tfvars -refresh=true") {
		t.Errorf("plan call = %q", exec.calls[0])
	}
	if !strings.Contains(h.stderr.String(), "prod: 3 to delete") {
		t.Errorf("workspace should default to base of --dir: %s", h.stderr)
	}

	// A saved plan file is only converted, never re-planned.
	exec.calls = nil
	h = newHarness()
	h.app.Executor = exec
	if code := h.run("check", "--plan-file", "saved.tfplan", "--format", "none"); code != ExitOK {
		t.Fatalf("exit = %d: %s", code, h.stderr)
	}
	if len(exec.calls) != 1 || !strings.Contains(exec.calls[0], "terraform show -json -no-color") || !strings.HasSuffix(exec.calls[0], "saved.tfplan") {
		t.Errorf("calls = %v", exec.calls)
	}
}

func TestVersionAndHelp(t *testing.T) {
	h := newHarness()
	if code := h.run("version"); code != ExitOK || h.stdout.String() != "driftwatch 1.2.3\n" {
		t.Errorf("version: %d %q", code, h.stdout)
	}
	h = newHarness()
	if code := h.run("help"); code != ExitOK || !strings.Contains(h.stdout.String(), "driftwatch check") {
		t.Errorf("help: %d %q", code, h.stdout)
	}
	h = newHarness()
	if code := h.run("check", "-h"); code != ExitOK || !strings.Contains(h.stderr.String(), "-ignore-file") {
		t.Errorf("check -h: %d %q", code, h.stderr)
	}
}

func TestSplitLabels(t *testing.T) {
	tests := map[string]string{
		"driftwatch,terraform-drift": "driftwatch|terraform-drift",
		" a , ,b ":                   "a|b",
		"":                           "",
	}
	for in, want := range tests {
		if got := strings.Join(splitLabels(in), "|"); got != want {
			t.Errorf("splitLabels(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckWarnsAboutUnusedRulesAndIncompletePlans(t *testing.T) {
	rules := filepath.Join(t.TempDir(), "rules")
	if err := os.WriteFile(rules, []byte("terraform_data.metadata input.owner\naws_instance.* tags[\"LastScannedAt\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness()
	if code := h.run("check", "--plan-json", plan("update-only.json"), "--ignore-file", rules, "--format", "none"); code != ExitOK {
		t.Fatalf("exit = %d: %s", code, h.stderr)
	}
	if want := `note: ignore rule matched nothing in this plan: line 2: aws_instance.* tags["LastScannedAt"]`; !strings.Contains(h.stderr.String(), want) {
		t.Errorf("stderr lacks %q:\n%s", want, h.stderr)
	}
	if strings.Contains(h.stderr.String(), "line 1") {
		t.Errorf("a rule that matched was reported unused:\n%s", h.stderr)
	}

	h = newHarness()
	if code := h.run("check", "--plan-json", plan("deferred.json"), "--format", "json", "--exit-code"); code != ExitDrift {
		t.Fatalf("exit = %d: %s", code, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), "warning: the plan is incomplete; Terraform deferred 2 changes") {
		t.Errorf("stderr lacks the incomplete-plan warning:\n%s", h.stderr)
	}
	var rep struct {
		PlanIncomplete bool `json:"plan_incomplete"`
		Deferred       []struct {
			Address string `json:"address"`
		} `json:"deferred_changes"`
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &rep); err != nil || !rep.PlanIncomplete || len(rep.Deferred) != 2 {
		t.Errorf("json report = %+v, %v", rep, err)
	}
}

func TestResolveVersion(t *testing.T) {
	info := func(version string, settings ...string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			bi := &debug.BuildInfo{Main: debug.Module{Version: version}}
			for i := 0; i+1 < len(settings); i += 2 {
				bi.Settings = append(bi.Settings, debug.BuildSetting{Key: settings[i], Value: settings[i+1]})
			}
			return bi, true
		}
	}
	const rev = "7cb5d49a1b2c3d4e5f60718293a4b5c6d7e8f901"
	tests := []struct {
		name    string
		ldflags string
		read    func() (*debug.BuildInfo, bool)
		want    string
	}{
		{"ldflags wins", "v9.9.9", info("v0.1.0"), "v9.9.9"},
		{"tagged module version", "", info("v0.1.0"), "v0.1.0"},
		{"tagged but dirty", "", info("v0.1.0+dirty"), "v0.1.0"},
		{"pseudo-version uses the commit", "", info("v0.1.1-0.20261108093000-7cb5d49a1b2c", "vcs.revision", rev), "dev+7cb5d49a1b2c"},
		{"pseudo-version on a dirty tree", "", info("v0.1.1-0.20261108093000-7cb5d49a1b2c+dirty", "vcs.revision", rev, "vcs.modified", "true"), "dev+7cb5d49a1b2c-dirty"},
		{"devel build with vcs info", "", info("(devel)", "vcs.revision", rev), "dev+7cb5d49a1b2c"},
		{"no vcs info", "", info("(devel)"), "dev"},
		{"no build info", "", func() (*debug.BuildInfo, bool) { return nil, false }, "dev"},
		{"pre-release tag is not a pseudo-version", "", info("v0.2.0-rc.1"), "v0.2.0-rc.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveVersion(tt.ldflags, tt.read); got != tt.want {
				t.Errorf("ResolveVersion = %q, want %q", got, tt.want)
			}
		})
	}
}
