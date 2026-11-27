// Package slack posts drift summaries to a Slack incoming webhook.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/builtbyshailendra/driftwatch/internal/drift"
	"github.com/builtbyshailendra/driftwatch/internal/report"
)

// MaxListedChanges caps how many resources a message lists individually.
const MaxListedChanges = 10

// Text is a Slack text object.
type Text struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Block is a Slack Block Kit block. Only header, section and context blocks
// are used.
type Block struct {
	Type     string `json:"type"`
	Text     *Text  `json:"text,omitempty"`
	Elements []Text `json:"elements,omitempty"`
}

// Message is the webhook payload. Text is the notification fallback.
type Message struct {
	Text   string  `json:"text"`
	Blocks []Block `json:"blocks,omitempty"`
}

// BuildMessage summarises a report for Slack. issueURL may be empty.
func BuildMessage(r *report.Report, issueURL string) Message {
	if !r.HasDrift {
		text := escape(fmt.Sprintf("No Terraform drift in %s.", r.Workspace))
		icon := ":white_check_mark: "
		if r.PlanIncomplete {
			text += escape(fmt.Sprintf(" The plan is incomplete: %d deferred changes may hide drift.", r.Summary.Deferred))
			icon = ":warning: "
		}
		return Message{
			Text:   text,
			Blocks: []Block{{Type: "section", Text: &Text{Type: "mrkdwn", Text: icon + text}}},
		}
	}
	// The fallback text is rendered as mrkdwn, so it needs escaping too.
	text := escape(fmt.Sprintf("Terraform drift in %s: %s", r.Workspace, r.Headline()))
	blocks := []Block{
		{Type: "header", Text: &Text{Type: "plain_text", Text: report.Truncate(r.Title(), 150)}},
		{Type: "section", Text: &Text{Type: "mrkdwn", Text: "*" + escape(r.Headline()) + "*"}},
	}

	var lines []string
	all := append(append([]changeLine{}, linesFor(r.OutsideChanges, "changed outside Terraform: ")...), linesFor(r.Changes, "")...)
	for i, l := range all {
		if i == MaxListedChanges {
			lines = append(lines, fmt.Sprintf("...and %d more", len(all)-MaxListedChanges))
			break
		}
		lines = append(lines, l.String())
	}
	if len(lines) > 0 {
		blocks = append(blocks, Block{Type: "section", Text: &Text{Type: "mrkdwn", Text: strings.Join(lines, "\n")}})
	}

	ctx := fmt.Sprintf("Workspace `%s` | Terraform %s", escape(r.Workspace), r.TerraformVersion)
	if r.PlanIncomplete {
		ctx += fmt.Sprintf(" | plan incomplete, %d deferred", r.Summary.Deferred)
	}
	if r.Summary.Ignored > 0 {
		ctx += fmt.Sprintf(" | %d ignored by rules", r.Summary.Ignored)
	}
	if issueURL != "" {
		ctx += " | <" + issueURL + "|tracking issue>"
	}
	blocks = append(blocks, Block{Type: "context", Elements: []Text{{Type: "mrkdwn", Text: ctx}}})
	return Message{Text: text, Blocks: blocks}
}

type changeLine struct {
	prefix, kind, address string
}

func (l changeLine) String() string {
	return fmt.Sprintf("- %s*%s* `%s`", l.prefix, l.kind, escape(l.address))
}

func linesFor(changes []drift.Change, prefix string) []changeLine {
	out := make([]changeLine, len(changes))
	for i, c := range changes {
		out[i] = changeLine{prefix: prefix, kind: string(c.Kind), address: c.Address}
	}
	return out
}

// escape applies Slack's mrkdwn escaping rules for &, < and >.
func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// Notifier posts messages to an incoming webhook URL.
type Notifier struct {
	WebhookURL string
	HTTP       *http.Client
}

// NewNotifier returns a notifier with a request timeout.
func NewNotifier(webhookURL string) *Notifier {
	return &Notifier{WebhookURL: webhookURL, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// Send posts msg. Slack answers 200 with body "ok" on success.
func (n *Notifier) Send(ctx context.Context, msg Message) error {
	if n.WebhookURL == "" {
		return fmt.Errorf("slack: webhook URL is empty")
	}
	buf, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("slack: encode message: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.WebhookURL, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("slack: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := n.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// The webhook URL is a secret; never include it in errors.
		return fmt.Errorf("slack: post to webhook failed: %w", redact(err, n.WebhookURL))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack: webhook returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

type redactedError struct{ msg string }

func (e redactedError) Error() string { return e.msg }

func redact(err error, secret string) error {
	if secret == "" {
		return err
	}
	return redactedError{msg: strings.ReplaceAll(err.Error(), secret, "[webhook URL]")}
}
