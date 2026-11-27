// Package github keeps one GitHub issue per Terraform workspace in sync with
// the latest drift report, using the GitHub REST API.
//
// The issue is identified by a hidden HTML comment in its body rather than by
// title, so people can rename the issue freely.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the public GitHub REST API endpoint. GitHub Enterprise
// Server users pass https://HOST/api/v3 instead.
const DefaultBaseURL = "https://api.github.com"

// MaxBodyLength is GitHub's limit on issue and comment bodies.
const MaxBodyLength = 65536

const (
	perPage  = 100
	maxPages = 10
)

// Client is a minimal GitHub REST client.
type Client struct {
	BaseURL   string
	Token     string
	UserAgent string
	HTTP      *http.Client
}

// NewClient returns a client with sensible defaults.
func NewClient(baseURL, token string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		Token:     token,
		UserAgent: "driftwatch",
		HTTP:      &http.Client{Timeout: 30 * time.Second},
	}
}

// Issue is the subset of the GitHub issue object driftwatch uses.
type Issue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	HTMLURL     string    `json:"html_url"`
	PullRequest *struct{} `json:"pull_request,omitempty"`
}

// APIError is returned for non-2xx responses.
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("github: %s %s: %d %s", e.Method, e.Path, e.StatusCode, e.Message)
}

// Repo is an owner/name pair.
type Repo struct {
	Owner string
	Name  string
}

// ParseRepo parses "owner/name".
func ParseRepo(s string) (Repo, error) {
	owner, name, ok := strings.Cut(s, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return Repo{}, fmt.Errorf("repository must look like owner/name, got %q", s)
	}
	return Repo{Owner: owner, Name: name}, nil
}

func (r Repo) String() string { return r.Owner + "/" + r.Name }

func (r Repo) path(suffix string) string {
	return "/repos/" + url.PathEscape(r.Owner) + "/" + url.PathEscape(r.Name) + suffix
}

// Marker returns the hidden comment that identifies a workspace's issue.
func Marker(workspace string) string {
	// "--" would end the HTML comment early, so collapse runs of dashes.
	for strings.Contains(workspace, "--") {
		workspace = strings.ReplaceAll(workspace, "--", "-")
	}
	return "<!-- driftwatch:workspace=" + workspace + " -->"
}

// FindIssue returns the first open issue carrying label whose body contains
// marker, or nil if there is none. Pull requests are skipped.
func (c *Client) FindIssue(ctx context.Context, repo Repo, label, marker string) (*Issue, error) {
	for page := 1; page <= maxPages; page++ {
		q := url.Values{}
		q.Set("state", "open")
		q.Set("labels", label)
		q.Set("per_page", fmt.Sprint(perPage))
		q.Set("page", fmt.Sprint(page))
		var issues []Issue
		if err := c.do(ctx, http.MethodGet, repo.path("/issues?"+q.Encode()), nil, &issues); err != nil {
			return nil, err
		}
		for i := range issues {
			if issues[i].PullRequest == nil && strings.Contains(issues[i].Body, marker) {
				return &issues[i], nil
			}
		}
		if len(issues) < perPage {
			return nil, nil
		}
	}
	return nil, nil
}

// CreateIssue opens a new issue.
func (c *Client) CreateIssue(ctx context.Context, repo Repo, title, body string, labels []string) (*Issue, error) {
	payload := map[string]any{"title": title, "body": body, "labels": labels}
	var out Issue
	if err := c.do(ctx, http.MethodPost, repo.path("/issues"), payload, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateIssue replaces the title and body of an issue.
func (c *Client) UpdateIssue(ctx context.Context, repo Repo, number int, title, body string) (*Issue, error) {
	payload := map[string]any{"title": title, "body": body}
	var out Issue
	if err := c.do(ctx, http.MethodPatch, repo.path(fmt.Sprintf("/issues/%d", number)), payload, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Comment adds a comment to an issue.
func (c *Client) Comment(ctx context.Context, repo Repo, number int, body string) error {
	payload := map[string]any{"body": body}
	return c.do(ctx, http.MethodPost, repo.path(fmt.Sprintf("/issues/%d/comments", number)), payload, nil)
}

// CloseIssue closes an issue as completed.
func (c *Client) CloseIssue(ctx context.Context, repo Repo, number int) (*Issue, error) {
	payload := map[string]any{"state": "closed", "state_reason": "completed"}
	var out Issue
	if err := c.do(ctx, http.MethodPatch, repo.path(fmt.Sprintf("/issues/%d", number)), payload, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) do(ctx context.Context, method, path string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("github: encode request: %w", err)
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return fmt.Errorf("github: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("github: %s %s: %w", method, stripQuery(path), err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("github: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := &APIError{Method: method, Path: stripQuery(path), StatusCode: resp.StatusCode}
		var msg struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &msg) == nil && msg.Message != "" {
			apiErr.Message = msg.Message
		} else {
			apiErr.Message = http.StatusText(resp.StatusCode)
		}
		return apiErr
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("github: decode %s %s response: %w", method, stripQuery(path), err)
	}
	return nil
}

func stripQuery(p string) string {
	before, _, _ := strings.Cut(p, "?")
	return before
}

// SyncAction says what Sync did.
type SyncAction string

// Possible outcomes of Sync.
const (
	ActionCreated SyncAction = "created"
	ActionUpdated SyncAction = "updated"
	ActionClosed  SyncAction = "closed"
	ActionNone    SyncAction = "none"
)

// SyncRequest describes the desired issue state for one workspace.
type SyncRequest struct {
	Repo      Repo
	Workspace string
	Title     string
	Body      string
	Labels    []string
	HasDrift  bool
	// CloseResolved closes the open issue when the workspace has no drift.
	CloseResolved bool
}

// SyncResult reports what Sync changed.
type SyncResult struct {
	Action SyncAction
	Issue  *Issue
}

// ErrNoLabels is returned when SyncRequest has no labels: the first label is
// what FindIssue filters on.
var ErrNoLabels = errors.New("github: at least one issue label is required")

// Sync opens, updates or closes the workspace's drift issue.
//
//   - drift and no open issue: create one
//   - drift and an open issue: replace its title and body
//   - no drift and an open issue: comment and close it, if CloseResolved
//   - no drift and no open issue: nothing
func (c *Client) Sync(ctx context.Context, req SyncRequest) (SyncResult, error) {
	if len(req.Labels) == 0 {
		return SyncResult{}, ErrNoLabels
	}
	marker := Marker(req.Workspace)
	existing, err := c.FindIssue(ctx, req.Repo, req.Labels[0], marker)
	if err != nil {
		return SyncResult{}, err
	}
	body := LimitBody(req.Body, marker)

	switch {
	case req.HasDrift && existing == nil:
		issue, err := c.CreateIssue(ctx, req.Repo, req.Title, body, req.Labels)
		if err != nil {
			return SyncResult{}, err
		}
		return SyncResult{Action: ActionCreated, Issue: issue}, nil
	case req.HasDrift:
		issue, err := c.UpdateIssue(ctx, req.Repo, existing.Number, req.Title, body)
		if err != nil {
			return SyncResult{}, err
		}
		return SyncResult{Action: ActionUpdated, Issue: issue}, nil
	case existing != nil && req.CloseResolved:
		comment := LimitBody("Drift resolved: the latest check of `"+req.Workspace+"` found no changes. Closing.", "")
		if err := c.Comment(ctx, req.Repo, existing.Number, comment); err != nil {
			return SyncResult{}, err
		}
		issue, err := c.CloseIssue(ctx, req.Repo, existing.Number)
		if err != nil {
			return SyncResult{}, err
		}
		return SyncResult{Action: ActionClosed, Issue: issue}, nil
	default:
		return SyncResult{Action: ActionNone, Issue: existing}, nil
	}
}

// LimitBody appends the marker to body and truncates the result so it fits in
// a GitHub issue. The marker always survives truncation.
func LimitBody(body, marker string) string {
	const notice = "\n\n_Report truncated to fit GitHub's issue size limit; see the workflow artifact for the full report._"
	suffix := ""
	if marker != "" {
		suffix = "\n\n" + marker + "\n"
	}
	if len(body)+len(suffix) <= MaxBodyLength {
		return body + suffix
	}
	keep := MaxBodyLength - len(suffix) - len(notice)
	// Do not cut a multi-byte UTF-8 sequence in half.
	for keep > 0 && keep < len(body) && body[keep]&0xC0 == 0x80 {
		keep--
	}
	return body[:keep] + notice + suffix
}
