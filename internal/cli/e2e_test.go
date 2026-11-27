package cli

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/builtbyshailendra/driftwatch/internal/terraform"
)

// TestEndToEndWithTerraform drives a real terraform binary against a copy of
// testdata/workspaces/drift. It needs network access to download the null,
// random and local providers, so it only runs when DRIFTWATCH_E2E=1.
func TestEndToEndWithTerraform(t *testing.T) {
	if os.Getenv("DRIFTWATCH_E2E") != "1" {
		t.Skip("set DRIFTWATCH_E2E=1 to run against a real terraform binary")
	}
	tf, err := exec.LookPath("terraform")
	if err != nil {
		t.Skip("terraform not on PATH")
	}
	dir := t.TempDir()
	copyTree(t, filepath.Join("..", "..", "testdata", "workspaces", "drift"), dir)

	ctx := context.Background()
	ex := terraform.ExecExecutor{}
	for _, args := range [][]string{
		{"init", "-input=false", "-no-color"},
		{"apply", "-input=false", "-no-color", "-auto-approve"},
	} {
		out, err := ex.Run(ctx, dir, tf, args...)
		if err != nil || out.ExitCode != 0 {
			t.Fatalf("terraform %s: %v\n%s", args[0], err, out.Stderr)
		}
	}

	h := newHarness()
	h.app.Executor = ex
	if code := h.run("check", "--dir", dir, "--terraform", tf, "--exit-code", "--format", "none"); code != ExitOK {
		t.Fatalf("clean workspace: exit = %d: %s", code, h.stderr)
	}

	// Drift outside Terraform plus a changed variable.
	if err := os.Remove(filepath.Join(dir, "out", "inventory.json")); err != nil {
		t.Fatal(err)
	}
	h = newHarness()
	h.app.Executor = ex
	code := h.run("check", "--dir", dir, "--terraform", tf, "--workspace", "e2e", "--exit-code", "--format", "json",
		"--plan-arg=-var=owner=data-platform")
	if code != ExitDrift {
		t.Fatalf("drifted workspace: exit = %d: %s", code, h.stderr)
	}
	for _, want := range []string{`"kind": "update"`, `"address": "terraform_data.metadata"`, `"source": "outside"`, `"address": "local_file.inventory"`} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("report lacks %s", want)
		}
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if d.IsDir() {
			if d.Name() == ".terraform" || d.Name() == "out" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if strings.HasPrefix(d.Name(), "terraform.tfstate") || strings.HasSuffix(d.Name(), ".tfplan") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
