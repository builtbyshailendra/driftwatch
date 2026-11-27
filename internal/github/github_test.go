package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// fakeGitHub is an in-memory stand-in for the issues endpoints.
type fakeGitHub struct {
	mu       sync.Mutex
	issues   []Issue
	labels   map[int][]string
	requests []string
	comments map[int][]string
	failOn   string // "METHOD /path" that should return 500
}

func newFake(issues ...Issue) *fakeGitHub {
	f := &fakeGitHub{labels: map[int][]string{}, comments: map[int][]string{}}
	for _, is := range issues {
		f.issues = append(f.issues, is)
		f.labels[is.Number] = []string{"driftwatch"}
	}
	return f
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := r.Method + " " + r.URL.Path
	f.requests = append(f.requests, key)
	if r.Header.Get("Authorization") != "Bearer test-token" {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
		return
	}
	if key == f.failOn {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"message":"boom"}`)
		return
	}
	var payload map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&payload)
	}
	const prefix = "/repos/acme/infra/issues"
	switch {
	case r.Method == http.MethodGet && r.URL.Path == prefix:
		label := r.URL.Query().Get("labels")
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		var open []Issue
		for _, is := range f.issues {
			if is.State == "open" && contains(f.labels[is.Number], label) {
				open = append(open, is)
			}
		}
		start := (page - 1) * per
		end := min(start+per, len(open))
		if start > len(open) {
			start = len(open)
		}
		_ = json.NewEncoder(w).Encode(open[start:end])
	case r.Method == http.MethodPost && r.URL.Path == prefix:
		n := 1
		for _, existing := range f.issues {
			n = max(n, existing.Number+1)
		}
		is := Issue{Number: n, Title: payload["title"].(string), Body: payload["body"].(string), State: "open",
			HTMLURL: fmt.Sprintf("https://github.com/acme/infra/issues/%d", n)}
		for _, l := range payload["labels"].([]any) {
			f.labels[n] = append(f.labels[n], l.(string))
		}
		f.issues = append(f.issues, is)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(is)
	case strings.HasPrefix(r.URL.Path, prefix+"/"):
		rest := strings.TrimPrefix(r.URL.Path, prefix+"/")
		numStr, sub, _ := strings.Cut(rest, "/")
		n, _ := strconv.Atoi(numStr)
		is := f.find(n)
		if is == nil {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		switch {
		case r.Method == http.MethodPost && sub == "comments":
			f.comments[n] = append(f.comments[n], payload["body"].(string))
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":1}`)
		case r.Method == http.MethodPatch && sub == "":
			if v, ok := payload["title"].(string); ok {
				is.Title = v
			}
			if v, ok := payload["body"].(string); ok {
				is.Body = v
			}
			if v, ok := payload["state"].(string); ok {
				is.State = v
			}
			_ = json.NewEncoder(w).Encode(is)
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeGitHub) find(n int) *Issue {
	for i := range f.issues {
		if f.issues[i].Number == n {
			return &f.issues[i]
		}
	}
	return nil
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func newTestClient(t *testing.T, f *fakeGitHub) *Client {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL+"/", "test-token")
	c.HTTP = srv.Client()
	return c
}

var repo = Repo{Owner: "acme", Name: "infra"}

func TestParseRepo(t *testing.T) {
	tests := []struct {
		in      string
		want    Repo
		wantErr bool
	}{
		{"acme/infra", Repo{"acme", "infra"}, false},
		{"acme", Repo{}, true},
		{"/infra", Repo{}, true},
		{"acme/", Repo{}, true},
		{"acme/infra/extra", Repo{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseRepo(tt.in)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("ParseRepo(%q) = %v, %v", tt.in, got, err)
			}
		})
	}
	if repo.String() != "acme/infra" {
		t.Errorf("String() = %q", repo.String())
	}
}

func TestSync(t *testing.T) {
	marker := Marker("prod")
	existing := Issue{Number: 7, Title: "old", Body: "old body\n\n" + marker, State: "open"}
	otherWorkspace := Issue{Number: 3, Title: "staging", Body: "x " + Marker("staging"), State: "open"}

	tests := []struct {
		name         string
		issues       []Issue
		hasDrift     bool
		closeRes     bool
		wantAction   SyncAction
		wantRequests []string
		check        func(t *testing.T, f *fakeGitHub)
	}{
		{
			name: "drift without issue creates one", issues: []Issue{otherWorkspace}, hasDrift: true,
			wantAction:   ActionCreated,
			wantRequests: []string{"GET /repos/acme/infra/issues", "POST /repos/acme/infra/issues"},
			check: func(t *testing.T, f *fakeGitHub) {
				created := f.find(4)
				if created == nil || !strings.Contains(created.Body, marker) || !strings.HasPrefix(created.Body, "report") {
					t.Errorf("created issue = %+v", created)
				}
				if got := f.labels[4]; strings.Join(got, ",") != "driftwatch,terraform-drift" {
					t.Errorf("labels = %v", got)
				}
			},
		},
		{
			name: "drift with issue updates it", issues: []Issue{otherWorkspace, existing}, hasDrift: true,
			wantAction:   ActionUpdated,
			wantRequests: []string{"GET /repos/acme/infra/issues", "PATCH /repos/acme/infra/issues/7"},
			check: func(t *testing.T, f *fakeGitHub) {
				if is := f.find(7); is.Title != "Drift in prod" || !strings.Contains(is.Body, marker) {
					t.Errorf("updated issue = %+v", is)
				}
			},
		},
		{
			name: "no drift closes the issue", issues: []Issue{existing}, closeRes: true,
			wantAction: ActionClosed,
			wantRequests: []string{
				"GET /repos/acme/infra/issues",
				"POST /repos/acme/infra/issues/7/comments",
				"PATCH /repos/acme/infra/issues/7",
			},
			check: func(t *testing.T, f *fakeGitHub) {
				if f.find(7).State != "closed" || len(f.comments[7]) != 1 {
					t.Errorf("issue = %+v comments = %v", f.find(7), f.comments[7])
				}
			},
		},
		{
			name: "no drift leaves issue open when closing is disabled", issues: []Issue{existing},
			wantAction:   ActionNone,
			wantRequests: []string{"GET /repos/acme/infra/issues"},
		},
		{
			name:         "no drift and no issue does nothing",
			wantAction:   ActionNone,
			wantRequests: []string{"GET /repos/acme/infra/issues"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(tt.issues...)
			c := newTestClient(t, f)
			res, err := c.Sync(context.Background(), SyncRequest{
				Repo: repo, Workspace: "prod", Title: "Drift in prod", Body: "report",
				Labels: []string{"driftwatch", "terraform-drift"}, HasDrift: tt.hasDrift, CloseResolved: tt.closeRes,
			})
			if err != nil {
				t.Fatal(err)
			}
			if res.Action != tt.wantAction {
				t.Errorf("action = %s, want %s", res.Action, tt.wantAction)
			}
			if strings.Join(f.requests, "\n") != strings.Join(tt.wantRequests, "\n") {
				t.Errorf("requests:\n%s\nwant:\n%s", strings.Join(f.requests, "\n"), strings.Join(tt.wantRequests, "\n"))
			}
			if tt.check != nil {
				tt.check(t, f)
			}
		})
	}
}

func TestFindIssuePaginatesAndSkipsPullRequests(t *testing.T) {
	var issues []Issue
	for i := 1; i <= 150; i++ {
		issues = append(issues, Issue{Number: i, Body: "unrelated", State: "open"})
	}
	issues[140].Body = "report " + Marker("prod")
	f := newFake(issues...)
	c := newTestClient(t, f)
	got, err := c.FindIssue(context.Background(), repo, "driftwatch", Marker("prod"))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Number != 141 {
		t.Fatalf("FindIssue = %+v, want #141", got)
	}
	if len(f.requests) != 2 {
		t.Errorf("requests = %v, want two pages", f.requests)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[{"number":9,"body":"`+strings.ReplaceAll(Marker("prod"), `"`, `\"`)+`","pull_request":{}}]`)
	}))
	defer srv.Close()
	c2 := NewClient(srv.URL, "t")
	got, err = c2.FindIssue(context.Background(), repo, "driftwatch", Marker("prod"))
	if err != nil || got != nil {
		t.Errorf("pull request matched: %+v, %v", got, err)
	}
}

func TestAPIErrors(t *testing.T) {
	f := newFake(Issue{Number: 1, Body: Marker("prod"), State: "open"})
	f.failOn = "PATCH /repos/acme/infra/issues/1"
	c := newTestClient(t, f)
	_, err := c.Sync(context.Background(), SyncRequest{Repo: repo, Workspace: "prod", Title: "t", Body: "b", Labels: []string{"driftwatch"}, HasDrift: true})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 || apiErr.Message != "boom" || apiErr.Path != "/repos/acme/infra/issues/1" {
		t.Fatalf("error = %#v", err)
	}

	bad := newTestClient(t, newFake())
	bad.Token = "wrong"
	_, err = bad.FindIssue(context.Background(), repo, "driftwatch", "m")
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized || !strings.Contains(err.Error(), "Bad credentials") {
		t.Errorf("auth error = %v", err)
	}

	if _, err := c.Sync(context.Background(), SyncRequest{Repo: repo}); !errors.Is(err, ErrNoLabels) {
		t.Errorf("missing labels error = %v", err)
	}
}

func TestRequestHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = io.WriteString(w, `{"number":1}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "tok")
	if _, err := c.CreateIssue(context.Background(), repo, "t", "b", []string{"x"}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Authorization":        "Bearer tok",
		"Accept":               "application/vnd.github+json",
		"X-Github-Api-Version": "2022-11-28",
		"Content-Type":         "application/json",
		"User-Agent":           "driftwatch",
	}
	for k, v := range want {
		if got.Get(k) != v {
			t.Errorf("header %s = %q, want %q", k, got.Get(k), v)
		}
	}
}

func TestLimitBody(t *testing.T) {
	marker := Marker("prod")
	tests := []struct {
		name      string
		body      string
		truncated bool
	}{
		{"small body", "hello", false},
		{"exactly at limit", strings.Repeat("a", MaxBodyLength-len(marker)-3), false},
		{"over limit", strings.Repeat("a", MaxBodyLength*2), true},
		{"multibyte cut", strings.Repeat("é", MaxBodyLength), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LimitBody(tt.body, marker)
			if len(got) > MaxBodyLength {
				t.Errorf("length %d exceeds limit", len(got))
			}
			if !strings.Contains(got, marker) {
				t.Error("marker lost")
			}
			if strings.Contains(got, "truncated") != tt.truncated {
				t.Errorf("truncated = %v, want %v", !tt.truncated, tt.truncated)
			}
			if !utf8.ValidString(got) {
				t.Error("result is not valid UTF-8")
			}
		})
	}
}

func TestMarkerIsStableAndCommentSafe(t *testing.T) {
	if Marker("prod") != "<!-- driftwatch:workspace=prod -->" {
		t.Errorf("Marker = %q", Marker("prod"))
	}
	if strings.Contains(strings.TrimSuffix(strings.TrimPrefix(Marker("a---b--c"), "<!--"), "-->"), "--") {
		t.Error("marker body must not contain -- which ends an HTML comment early")
	}
}
