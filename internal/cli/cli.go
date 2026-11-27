// Package cli implements the driftwatch command line.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/builtbyshailendra/driftwatch/internal/drift"
	"github.com/builtbyshailendra/driftwatch/internal/github"
	"github.com/builtbyshailendra/driftwatch/internal/ignore"
	"github.com/builtbyshailendra/driftwatch/internal/report"
	"github.com/builtbyshailendra/driftwatch/internal/slack"
	"github.com/builtbyshailendra/driftwatch/internal/terraform"
	"github.com/builtbyshailendra/driftwatch/internal/tfplan"
)

// Process exit codes.
const (
	ExitOK    = 0
	ExitError = 1
	ExitDrift = 2
)

// DefaultIgnoreFile is picked up from the Terraform directory when
// --ignore-file is not given.
const DefaultIgnoreFile = ".driftignore"

// App holds the process dependencies so tests can replace them.
type App struct {
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	Getenv   func(string) string
	HTTP     *http.Client
	Executor terraform.Executor
	Now      func() time.Time
	Version  string
}

// NewApp returns an App wired to the real process environment.
func NewApp(version string) *App {
	return &App{
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Getenv:   os.Getenv,
		HTTP:     &http.Client{Timeout: 30 * time.Second},
		Executor: terraform.ExecExecutor{},
		Now:      time.Now,
		Version:  version,
	}
}

const usage = `driftwatch detects Terraform drift and reports it.

Usage:
  driftwatch check [flags]   run or read a plan and report drift
  driftwatch version         print the version

Run "driftwatch check -h" for the check flags.
`

// Run executes the command line and returns the process exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.Stderr, usage)
		return ExitError
	}
	switch args[0] {
	case "check":
		return a.check(ctx, args[1:])
	case "version", "--version", "-version":
		fmt.Fprintf(a.Stdout, "driftwatch %s\n", a.Version)
		return ExitOK
	case "help", "-h", "--help":
		fmt.Fprint(a.Stdout, usage)
		return ExitOK
	default:
		fmt.Fprintf(a.Stderr, "driftwatch: unknown command %q\n\n%s", args[0], usage)
		return ExitError
	}
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, " ") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

type checkOptions struct {
	dir           string
	planFile      string
	planJSON      string
	terraformBin  string
	planArgs      stringList
	workspace     string
	ignoreFile    string
	format        string
	markdownOut   string
	jsonOut       string
	githubIssue   bool
	githubRepo    string
	githubLabels  string
	closeResolved bool
	slack         bool
	slackOnClean  bool
	exitCode      bool
}

func (a *App) parseCheck(args []string) (*checkOptions, error) {
	o := &checkOptions{}
	fs := flag.NewFlagSet("driftwatch check", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	fs.StringVar(&o.dir, "dir", ".", "Terraform working directory (must already be initialised)")
	fs.StringVar(&o.planFile, "plan-file", "", "saved plan from \"terraform plan -out\"; converted with terraform show -json")
	fs.StringVar(&o.planJSON, "plan-json", "", "JSON plan from \"terraform show -json\"; \"-\" reads stdin")
	fs.StringVar(&o.terraformBin, "terraform", envOr(a.Getenv, "TERRAFORM", "terraform"), "terraform binary (env TERRAFORM)")
	fs.Var(&o.planArgs, "plan-arg", "extra argument for terraform plan, repeatable (e.g. -plan-arg=-var-file=prod.tfvars)")
	fs.StringVar(&o.workspace, "workspace", "", "name used in reports and to identify the GitHub issue (default: base name of --dir)")
	fs.StringVar(&o.ignoreFile, "ignore-file", "", "ignore rules file (default: "+DefaultIgnoreFile+" in --dir when present)")
	fs.StringVar(&o.format, "format", "markdown", "report printed to stdout: markdown, json or none")
	fs.StringVar(&o.markdownOut, "markdown-out", "", "also write the Markdown report to this file")
	fs.StringVar(&o.jsonOut, "json-out", "", "also write the JSON report to this file")
	fs.BoolVar(&o.githubIssue, "github-issue", false, "open, update or close a GitHub issue (needs GITHUB_TOKEN)")
	fs.StringVar(&o.githubRepo, "github-repo", a.Getenv("GITHUB_REPOSITORY"), "repository for the issue, owner/name (env GITHUB_REPOSITORY)")
	fs.StringVar(&o.githubLabels, "github-labels", "driftwatch,terraform-drift", "comma-separated issue labels; the first one is used to find the issue")
	fs.BoolVar(&o.closeResolved, "close-resolved", true, "close the open drift issue when drift is gone")
	fs.BoolVar(&o.slack, "slack", false, "post a summary to the Slack webhook in SLACK_WEBHOOK_URL when drift is found")
	fs.BoolVar(&o.slackOnClean, "slack-on-clean", false, "with --slack, also post when there is no drift")
	fs.BoolVar(&o.exitCode, "exit-code", false, "exit with code 2 when drift is found")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if o.planFile != "" && o.planJSON != "" {
		return nil, errors.New("--plan-file and --plan-json are mutually exclusive")
	}
	switch o.format {
	case "markdown", "json", "none":
	default:
		return nil, fmt.Errorf("--format must be markdown, json or none, got %q", o.format)
	}
	if o.workspace == "" {
		o.workspace = defaultWorkspace(o.dir)
	}
	return o, nil
}

func (a *App) check(ctx context.Context, args []string) int {
	o, err := a.parseCheck(args)
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	if err != nil {
		fmt.Fprintf(a.Stderr, "driftwatch: %v\n", err)
		return ExitError
	}

	rep, err := a.buildReport(ctx, o)
	if err != nil {
		fmt.Fprintf(a.Stderr, "driftwatch: %v\n", err)
		return ExitError
	}
	for _, r := range rep.UnusedIgnoreRules {
		fmt.Fprintf(a.Stderr, "driftwatch: note: ignore rule matched nothing in this plan: %s\n", r)
	}
	if rep.PlanIncomplete {
		fmt.Fprintf(a.Stderr, "driftwatch: warning: the plan is incomplete; Terraform deferred %d changes, so the report may not show all drift\n", rep.Summary.Deferred)
	}
	if err := a.writeReports(o, rep); err != nil {
		fmt.Fprintf(a.Stderr, "driftwatch: %v\n", err)
		return ExitError
	}

	failed := false
	issueURL := ""
	if o.githubIssue {
		url, err := a.syncIssue(ctx, o, rep)
		if err != nil {
			fmt.Fprintf(a.Stderr, "driftwatch: %v\n", err)
			failed = true
		}
		issueURL = url
	}
	if o.slack && (rep.HasDrift || o.slackOnClean) {
		if err := a.postSlack(ctx, rep, issueURL); err != nil {
			fmt.Fprintf(a.Stderr, "driftwatch: %v\n", err)
			failed = true
		}
	}

	fmt.Fprintf(a.Stderr, "driftwatch: %s: %s\n", rep.Workspace, rep.Headline())
	switch {
	case failed:
		return ExitError
	case rep.HasDrift && o.exitCode:
		return ExitDrift
	default:
		return ExitOK
	}
}

func (a *App) buildReport(ctx context.Context, o *checkOptions) (*report.Report, error) {
	planJSON, err := a.loadPlan(ctx, o)
	if err != nil {
		return nil, err
	}
	plan, err := tfplan.ParseBytes(planJSON)
	if err != nil {
		return nil, err
	}
	rules, err := loadRules(o)
	if err != nil {
		return nil, err
	}
	res, err := drift.Analyze(plan, rules)
	if err != nil {
		return nil, err
	}
	return report.New(res, o.workspace, a.Now()), nil
}

func (a *App) loadPlan(ctx context.Context, o *checkOptions) ([]byte, error) {
	switch {
	case o.planJSON == "-":
		b, err := io.ReadAll(a.Stdin)
		if err != nil {
			return nil, fmt.Errorf("read plan JSON from stdin: %w", err)
		}
		return b, nil
	case o.planJSON != "":
		b, err := os.ReadFile(o.planJSON)
		if err != nil {
			return nil, fmt.Errorf("read plan JSON: %w", err)
		}
		return b, nil
	}
	runner := &terraform.Runner{Bin: o.terraformBin, Dir: o.dir, Exec: a.Executor}
	if o.planFile != "" {
		abs, err := filepath.Abs(o.planFile)
		if err != nil {
			return nil, fmt.Errorf("resolve plan file: %w", err)
		}
		return runner.Show(ctx, abs)
	}
	res, err := runner.Plan(ctx, o.planArgs)
	if err != nil {
		return nil, err
	}
	return res.JSON, nil
}

func loadRules(o *checkOptions) (*ignore.Rules, error) {
	if o.ignoreFile != "" {
		return ignore.Load(o.ignoreFile)
	}
	candidate := filepath.Join(o.dir, DefaultIgnoreFile)
	if _, err := os.Stat(candidate); err == nil {
		return ignore.Load(candidate)
	}
	return ignore.New(), nil
}

func (a *App) writeReports(o *checkOptions, rep *report.Report) error {
	switch o.format {
	case "markdown":
		if err := rep.WriteMarkdown(a.Stdout); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
	case "json":
		if err := rep.WriteJSON(a.Stdout); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
	}
	if o.markdownOut != "" {
		if err := writeFile(o.markdownOut, rep.WriteMarkdown); err != nil {
			return err
		}
	}
	if o.jsonOut != "" {
		if err := writeFile(o.jsonOut, rep.WriteJSON); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(path string, render func(io.Writer) error) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create report file: %w", err)
	}
	if err := render(f); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

func (a *App) syncIssue(ctx context.Context, o *checkOptions, rep *report.Report) (string, error) {
	token := a.Getenv("GITHUB_TOKEN")
	if token == "" {
		return "", errors.New("--github-issue needs a token in GITHUB_TOKEN")
	}
	repo, err := github.ParseRepo(o.githubRepo)
	if err != nil {
		return "", fmt.Errorf("--github-repo: %w", err)
	}
	client := github.NewClient(a.Getenv("GITHUB_API_URL"), token)
	if a.HTTP != nil {
		client.HTTP = a.HTTP
	}
	res, err := client.Sync(ctx, github.SyncRequest{
		Repo:          repo,
		Workspace:     rep.Workspace,
		Title:         rep.Title(),
		Body:          rep.Markdown(),
		Labels:        splitLabels(o.githubLabels),
		HasDrift:      rep.HasDrift,
		CloseResolved: o.closeResolved,
	})
	if err != nil {
		return "", err
	}
	if res.Issue != nil {
		fmt.Fprintf(a.Stderr, "driftwatch: issue #%d %s: %s\n", res.Issue.Number, res.Action, res.Issue.HTMLURL)
		if rep.HasDrift {
			return res.Issue.HTMLURL, nil
		}
	}
	return "", nil
}

func (a *App) postSlack(ctx context.Context, rep *report.Report, issueURL string) error {
	webhook := a.Getenv("SLACK_WEBHOOK_URL")
	if webhook == "" {
		return errors.New("--slack needs a webhook URL in SLACK_WEBHOOK_URL")
	}
	n := slack.NewNotifier(webhook)
	if a.HTTP != nil {
		n.HTTP = a.HTTP
	}
	if err := n.Send(ctx, slack.BuildMessage(rep, issueURL)); err != nil {
		return err
	}
	fmt.Fprintln(a.Stderr, "driftwatch: posted summary to Slack")
	return nil
}

func splitLabels(s string) []string {
	var out []string
	for _, l := range strings.Split(s, ",") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func defaultWorkspace(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return filepath.Base(abs)
}

func envOr(getenv func(string) string, key, fallback string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return fallback
}
