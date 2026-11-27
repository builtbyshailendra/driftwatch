package terraform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// fakeExec scripts the responses of terraform plan and terraform show.
type fakeExec struct {
	plan, show       Output
	planErr, showErr error
	calls            [][]string
	dirs             []string
}

func (f *fakeExec) Run(_ context.Context, dir, name string, args ...string) (Output, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	f.dirs = append(f.dirs, dir)
	if args[0] == "plan" {
		return f.plan, f.planErr
	}
	return f.show, f.showErr
}

func TestRunnerPlan(t *testing.T) {
	planJSON := []byte(`{"format_version":"1.2"}`)
	tests := []struct {
		name     string
		fake     fakeExec
		wantCode int
		wantErr  string
		calls    int
	}{
		{"no changes", fakeExec{plan: Output{ExitCode: 0}, show: Output{Stdout: planJSON}}, 0, "", 2},
		{"changes", fakeExec{plan: Output{ExitCode: 2}, show: Output{Stdout: planJSON}}, 2, "", 2},
		{"plan error keeps stderr", fakeExec{plan: Output{ExitCode: 1, Stderr: []byte("Error: Missing required argument")}}, 0, "Missing required argument", 1},
		{"binary missing", fakeExec{planErr: errors.New("executable file not found")}, 0, "executable file not found", 1},
		{"show fails", fakeExec{plan: Output{ExitCode: 2}, show: Output{ExitCode: 1, Stderr: []byte("Error: plan file corrupt")}}, 0, "plan file corrupt", 2},
		{"show empty", fakeExec{plan: Output{ExitCode: 2}, show: Output{Stdout: []byte("  \n")}}, 0, "no output", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.fake
			r := &Runner{Bin: "tf", Dir: "infra", Exec: &f}
			res, err := r.Plan(context.Background(), []string{"-var-file=prod.tfvars"})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if res.ExitCode != tt.wantCode || string(res.JSON) != string(planJSON) {
					t.Errorf("result = %d %s", res.ExitCode, res.JSON)
				}
			}
			if len(f.calls) != tt.calls {
				t.Fatalf("calls = %v", f.calls)
			}
			plan := strings.Join(f.calls[0], " ")
			for _, want := range []string{"tf plan", "-input=false", "-detailed-exitcode", "-out=", "-var-file=prod.tfvars"} {
				if !strings.Contains(plan, want) {
					t.Errorf("plan command %q lacks %q", plan, want)
				}
			}
			if f.dirs[0] != "infra" {
				t.Errorf("dir = %q", f.dirs[0])
			}
			if len(f.calls) == 2 {
				outArg := ""
				for _, a := range f.calls[0] {
					if strings.HasPrefix(a, "-out=") {
						outArg = strings.TrimPrefix(a, "-out=")
					}
				}
				show := f.calls[1]
				if show[1] != "show" || show[2] != "-json" || show[len(show)-1] != outArg {
					t.Errorf("show command = %v, want it to read %s", show, outArg)
				}
				if _, err := os.Stat(outArg); !os.IsNotExist(err) {
					t.Errorf("temporary plan dir not cleaned up: %v", err)
				}
			}
		})
	}
}

func TestRunnerDefaults(t *testing.T) {
	r := &Runner{}
	if r.bin() != "terraform" {
		t.Errorf("bin = %q", r.bin())
	}
	if _, ok := r.exec().(ExecExecutor); !ok {
		t.Errorf("exec = %T", r.exec())
	}
}

func TestCommandErrorKeepsTail(t *testing.T) {
	var lines []string
	for i := 1; i <= 50; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	err := commandError("terraform plan", Output{ExitCode: 1, Stderr: []byte(strings.Join(lines, "\n"))})
	if strings.Contains(err.Error(), "line 30\n") || !strings.Contains(err.Error(), "line 50") {
		t.Errorf("error = %v", err)
	}
	if got := commandError("terraform show", Output{ExitCode: 3}).Error(); got != "terraform show exited with code 3" {
		t.Errorf("empty stderr error = %q", got)
	}
}

// TestHelperProcess is not a real test: ExecExecutor tests re-run the test
// binary with GO_WANT_HELPER_PROCESS set so it behaves like a tiny program.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	fmt.Fprint(os.Stdout, "out:"+os.Getenv("HELPER_ARG"))
	fmt.Fprint(os.Stderr, "err")
	code := 0
	if os.Getenv("HELPER_ARG") == "fail" {
		code = 2
	}
	os.Exit(code)
}

func TestExecExecutor(t *testing.T) {
	tests := []struct {
		arg      string
		wantCode int
	}{
		{"ok", 0},
		{"fail", 2},
	}
	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			e := ExecExecutor{Env: append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "HELPER_ARG="+tt.arg)}
			out, err := e.Run(context.Background(), ".", os.Args[0], "-test.run=TestHelperProcess")
			if err != nil {
				t.Fatal(err)
			}
			if out.ExitCode != tt.wantCode || string(out.Stdout) != "out:"+tt.arg || string(out.Stderr) != "err" {
				t.Errorf("out = %d %q %q", out.ExitCode, out.Stdout, out.Stderr)
			}
		})
	}
	_, err := ExecExecutor{}.Run(context.Background(), ".", "driftwatch-no-such-binary")
	if err == nil {
		t.Error("missing binary did not return an error")
	}
}
