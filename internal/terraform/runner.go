// Package terraform runs "terraform plan -detailed-exitcode -out" and
// "terraform show -json" to obtain a machine-readable plan.
package terraform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Plan exit codes from "terraform plan -detailed-exitcode".
const (
	ExitNoChanges = 0
	ExitError     = 1
	ExitChanges   = 2
)

// Output is the result of one command.
type Output struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Executor runs a program. It returns an error only when the program could
// not be started; a non-zero exit code is reported in Output.
type Executor interface {
	Run(ctx context.Context, dir, name string, args ...string) (Output, error)
}

// ExecExecutor runs real processes with os/exec.
type ExecExecutor struct {
	// Env, when non-nil, replaces the inherited environment.
	Env []string
}

// Run implements Executor.
func (e ExecExecutor) Run(ctx context.Context, dir, name string, args ...string) (Output, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if e.Env != nil {
		cmd.Env = e.Env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := Output{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return out, nil
	case errors.As(err, &exitErr):
		out.ExitCode = exitErr.ExitCode()
		return out, nil
	default:
		return out, err
	}
}

// Runner drives the terraform binary in one working directory.
type Runner struct {
	Bin  string
	Dir  string
	Exec Executor
}

// PlanResult carries the JSON plan and the plan command's exit code.
type PlanResult struct {
	JSON     []byte
	ExitCode int
}

// Plan runs terraform plan with -detailed-exitcode and -out into a temporary
// file, converts the saved plan with terraform show -json and removes the
// temporary file. extraArgs are appended to the plan command, for example
// "-var-file=prod.tfvars" or "-refresh-only".
func (r *Runner) Plan(ctx context.Context, extraArgs []string) (*PlanResult, error) {
	tmp, err := os.MkdirTemp("", "driftwatch-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)
	planFile := filepath.Join(tmp, "drift.tfplan")

	args := append([]string{
		"plan", "-input=false", "-no-color",
		"-detailed-exitcode", "-out=" + planFile,
	}, extraArgs...)
	out, err := r.exec().Run(ctx, r.Dir, r.bin(), args...)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", r.bin(), err)
	}
	if out.ExitCode != ExitNoChanges && out.ExitCode != ExitChanges {
		return nil, commandError("terraform plan", out)
	}
	js, err := r.Show(ctx, planFile)
	if err != nil {
		return nil, err
	}
	return &PlanResult{JSON: js, ExitCode: out.ExitCode}, nil
}

// Show converts a saved plan file to JSON with terraform show -json.
func (r *Runner) Show(ctx context.Context, planFile string) ([]byte, error) {
	out, err := r.exec().Run(ctx, r.Dir, r.bin(), "show", "-json", "-no-color", planFile)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", r.bin(), err)
	}
	if out.ExitCode != 0 {
		return nil, commandError("terraform show", out)
	}
	if len(bytes.TrimSpace(out.Stdout)) == 0 {
		return nil, errors.New("terraform show -json produced no output")
	}
	return out.Stdout, nil
}

func (r *Runner) bin() string {
	if r.Bin == "" {
		return "terraform"
	}
	return r.Bin
}

func (r *Runner) exec() Executor {
	if r.Exec == nil {
		return ExecExecutor{}
	}
	return r.Exec
}

// commandError keeps the last lines of stderr, where Terraform prints the
// actual error, without flooding logs with the whole plan.
func commandError(what string, out Output) error {
	msg := strings.TrimSpace(string(out.Stderr))
	lines := strings.Split(msg, "\n")
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	msg = strings.Join(lines, "\n")
	if msg == "" {
		return fmt.Errorf("%s exited with code %d", what, out.ExitCode)
	}
	return fmt.Errorf("%s exited with code %d:\n%s", what, out.ExitCode, msg)
}
