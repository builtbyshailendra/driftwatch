package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/builtbyshailendra/driftwatch/internal/drift"
	"github.com/builtbyshailendra/driftwatch/internal/report"
)

func driftReport(n int) *report.Report {
	res := &drift.Result{TerraformVersion: "1.17.1", Ignored: []drift.Ignored{{Address: "random_pet.x"}}}
	res.Outside = []drift.Change{{Address: "local_file.inventory", Kind: drift.KindDelete}}
	for i := 0; i < n; i++ {
		res.Changes = append(res.Changes, drift.Change{Address: fmt.Sprintf("null_resource.n%d", i), Kind: drift.KindUpdate})
	}
	return report.New(res, "prod<eu>", time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC))
}

func TestBuildMessage(t *testing.T) {
	tests := []struct {
		name         string
		report       *report.Report
		issueURL     string
		wantText     string
		wantContains []string
		wantMissing  []string
		wantBlocks   int
	}{
		{
			name:         "clean",
			report:       report.New(&drift.Result{TerraformVersion: "1.17.1"}, "prod", time.Now()),
			wantText:     "No Terraform drift in prod.",
			wantContains: []string{":white_check_mark:"},
			wantBlocks:   1,
		},
		{
			name:         "drift with issue link",
			report:       driftReport(2),
			issueURL:     "https://github.com/acme/infra/issues/4",
			wantText:     "Terraform drift in prod&lt;eu&gt;: 2 to update, 1 changed outside Terraform",
			wantContains: []string{"prod&lt;eu&gt;", "changed outside Terraform: *delete* `local_file.inventory`", "*update* `null_resource.n1`", "<https://github.com/acme/infra/issues/4|tracking issue>", "1 ignored by rules"},
			wantBlocks:   4,
		},
		{
			name:         "clean but incomplete plan",
			report:       report.New(&drift.Result{TerraformVersion: "1.17.1", Incomplete: true, Deferred: []drift.Deferred{{Address: "a.b"}}}, "prod", time.Now()),
			wantText:     "No Terraform drift in prod. The plan is incomplete: 1 deferred changes may hide drift.",
			wantContains: []string{":warning:"},
			wantMissing:  []string{":white_check_mark:"},
			wantBlocks:   1,
		},
		{
			name: "drift in an incomplete plan",
			report: report.New(&drift.Result{TerraformVersion: "1.17.1", Incomplete: true,
				Changes:  []drift.Change{{Address: "null_resource.a", Kind: drift.KindUpdate}},
				Deferred: []drift.Deferred{{Address: "a.b"}, {Address: "a.c"}}}, "prod", time.Now()),
			wantText:     "Terraform drift in prod: 1 to update (plan incomplete, 2 deferred)",
			wantContains: []string{"plan incomplete, 2 deferred"},
			wantBlocks:   4,
		},
		{
			name:         "long list is capped",
			report:       driftReport(15),
			wantContains: []string{"...and 6 more", "`null_resource.n8`"},
			wantMissing:  []string{"`null_resource.n9`", "tracking issue"},
			wantBlocks:   4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := BuildMessage(tt.report, tt.issueURL)
			if tt.wantText != "" && msg.Text != tt.wantText {
				t.Errorf("Text = %q, want %q", msg.Text, tt.wantText)
			}
			if len(msg.Blocks) != tt.wantBlocks {
				t.Errorf("blocks = %d, want %d", len(msg.Blocks), tt.wantBlocks)
			}
			raw, _ := json.Marshal(msg)
			var all strings.Builder
			for _, b := range msg.Blocks {
				if b.Text != nil {
					all.WriteString(b.Text.Text + "\n")
				}
				for _, e := range b.Elements {
					all.WriteString(e.Text + "\n")
				}
			}
			for _, s := range tt.wantContains {
				if !strings.Contains(all.String(), s) {
					t.Errorf("message lacks %q:\n%s", s, raw)
				}
			}
			for _, s := range tt.wantMissing {
				if strings.Contains(all.String(), s) {
					t.Errorf("message unexpectedly has %q", s)
				}
			}
		})
	}
}

func TestSend(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{"ok", http.StatusOK, "ok", ""},
		{"invalid payload", http.StatusBadRequest, "invalid_blocks", "webhook returned 400: invalid_blocks"},
		{"revoked webhook", http.StatusForbidden, "invalid_token", "403"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotCT string
			var got Message
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotCT = r.Header.Get("Content-Type")
				_ = json.NewDecoder(r.Body).Decode(&got)
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer srv.Close()
			n := NewNotifier(srv.URL + "/services/T000/B000/XXXX")
			err := n.Send(context.Background(), Message{Text: "hello"})
			if tt.wantErr == "" && err != nil {
				t.Fatalf("Send: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
			if gotCT != "application/json" || got.Text != "hello" {
				t.Errorf("request content-type=%q payload=%+v", gotCT, got)
			}
		})
	}
}

func TestSendNeverLeaksWebhookURL(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL + "/services/T000/B000/secret-part"
	srv.Close() // connection refused from now on
	err := NewNotifier(url).Send(context.Background(), Message{Text: "x"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "secret-part") {
		t.Errorf("error leaks webhook URL: %v", err)
	}
	if err := (&Notifier{}).Send(context.Background(), Message{}); err == nil {
		t.Error("empty webhook URL accepted")
	}
}

func TestEscape(t *testing.T) {
	if got := escape("a & <b>"); got != "a &amp; &lt;b&gt;" {
		t.Errorf("escape = %q", got)
	}
}
