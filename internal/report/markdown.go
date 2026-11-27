package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/builtbyshailendra/driftwatch/internal/drift"
)

// MaxValueWidth caps how many characters of a value appear in a Markdown
// table cell. Long values such as rendered file contents are truncated.
const MaxValueWidth = 80

// WriteMarkdown renders the report as GitHub-flavoured Markdown.
func (r *Report) WriteMarkdown(w io.Writer) error {
	var b strings.Builder
	r.markdown(&b)
	_, err := io.WriteString(w, b.String())
	return err
}

// Markdown returns the rendered Markdown report.
func (r *Report) Markdown() string {
	var b strings.Builder
	r.markdown(&b)
	return b.String()
}

func (r *Report) markdown(b *strings.Builder) {
	fmt.Fprintf(b, "# Terraform drift report: %s\n\n", r.Workspace)
	status := "No drift detected."
	if r.HasDrift {
		status = "Drift detected: " + r.Headline() + "."
	}
	fmt.Fprintf(b, "**%s**\n\n", status)
	if r.PlanIncomplete {
		fmt.Fprintf(b, "> **Warning:** this plan is incomplete. Terraform deferred %d %s to a later run, "+
			"so the report may not show all drift. Apply and run the check again to see the rest.\n\n",
			r.Summary.Deferred, plural(r.Summary.Deferred, "change", "changes"))
	}
	fmt.Fprintf(b, "Generated %s with Terraform %s.\n\n",
		r.GeneratedAt.Format("2006-01-02 15:04 UTC"), orUnknown(r.TerraformVersion))

	b.WriteString("| Change | Count |\n|---|---:|\n")
	fmt.Fprintf(b, "| Delete | %d |\n", r.Summary.Delete)
	fmt.Fprintf(b, "| Replace | %d |\n", r.Summary.Replace)
	fmt.Fprintf(b, "| Update in place | %d |\n", r.Summary.Update)
	fmt.Fprintf(b, "| Create | %d |\n", r.Summary.Create)
	fmt.Fprintf(b, "| Changed outside Terraform | %d |\n", r.Summary.Outside)
	fmt.Fprintf(b, "| Ignored by rules | %d |\n", r.Summary.Ignored)
	if r.Summary.Deferred > 0 {
		fmt.Fprintf(b, "| Deferred to a later run | %d |\n", r.Summary.Deferred)
	}

	if len(r.OutsideChanges) > 0 {
		b.WriteString("\n## Changed outside Terraform\n\n")
		b.WriteString("Terraform found these objects changed since the last apply, without going through Terraform.\n")
		for _, c := range r.OutsideChanges {
			writeChange(b, c)
		}
	}

	if len(r.Changes) > 0 {
		b.WriteString("\n## Planned changes\n\n")
		b.WriteString("Applying the current configuration would make these changes.\n")
		for _, c := range r.Changes {
			writeChange(b, c)
		}
	}

	if len(r.Deferred) > 0 {
		b.WriteString("\n## Deferred changes\n\n")
		b.WriteString("Terraform could not plan these yet and will plan them in a later run.\n\n")
		b.WriteString("| Resource | Actions | Reason |\n|---|---|---|\n")
		for _, d := range r.Deferred {
			fmt.Fprintf(b, "| %s | %s | %s |\n", code(d.Address), strings.Join(d.Actions, ", "), d.Reason)
		}
	}

	if len(r.Ignored) > 0 {
		b.WriteString("\n## Ignored by rules\n\n")
		b.WriteString("| Resource | Change | Source | Rules |\n|---|---|---|---|\n")
		for _, ig := range r.Ignored {
			rules := make([]string, len(ig.Rules))
			for i, rule := range ig.Rules {
				rules[i] = code(rule)
			}
			fmt.Fprintf(b, "| %s | %s | %s | %s |\n", code(ig.Address), ig.Kind, ig.Source, strings.Join(rules, ", "))
		}
	}
}

func writeChange(b *strings.Builder, c drift.Change) {
	fmt.Fprintf(b, "\n### %s %s\n\n", code(c.Address), c.Kind)
	fmt.Fprintf(b, "Severity **%s**, provider `%s`", c.Severity, shortProvider(c.Provider))
	if c.Reason != "" {
		fmt.Fprintf(b, ", because %s", c.Reason)
	}
	b.WriteString(".\n")

	if len(c.Attributes) > 0 {
		b.WriteString("\n| Attribute | Before | After | Note |\n|---|---|---|---|\n")
		for _, a := range c.Attributes {
			fmt.Fprintf(b, "| %s | %s | %s | %s |\n",
				code(a.Path),
				cell(a.Before, a.BeforeSensitive, false),
				cell(a.After, a.AfterSensitive, a.AfterUnknown),
				note(a))
		}
	}
	if len(c.IgnoredAttributes) > 0 {
		b.WriteString("\nIgnored attributes: ")
		parts := make([]string, len(c.IgnoredAttributes))
		for i, ia := range c.IgnoredAttributes {
			parts[i] = fmt.Sprintf("%s (rule %s)", code(ia.Path), code(ia.Rule))
		}
		b.WriteString(strings.Join(parts, ", "))
		b.WriteString(".\n")
	}
}

func cell(v any, sensitive, unknown bool) string {
	if sensitive || unknown {
		return "_" + FormatValue(v, sensitive, unknown) + "_"
	}
	return code(Truncate(FormatValue(v, false, false), MaxValueWidth))
}

func note(a drift.Attribute) string {
	var notes []string
	if a.ForcesReplacement {
		notes = append(notes, "forces replacement")
	}
	if a.Sensitive() {
		notes = append(notes, "sensitive")
	}
	return strings.Join(notes, ", ")
}

// code wraps s in a Markdown code span that is safe inside a table cell.
func code(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	if strings.Contains(s, "`") {
		return "`` " + s + " ``"
	}
	return "`" + s + "`"
}

// Truncate shortens s to at most n runes, marking the cut with an ellipsis.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 3 {
		return string(r[:n])
	}
	return string(r[:n-3]) + "..."
}

// shortProvider turns registry.terraform.io/hashicorp/null into hashicorp/null.
func shortProvider(p string) string {
	return strings.TrimPrefix(p, "registry.terraform.io/")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func orUnknown(s string) string {
	if s == "" {
		return "(unknown version)"
	}
	return s
}
