package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
	// Side-effect import: registers concrete step factories into the
	// build package via init() in steps/register.go. Without this the
	// build.factories map is empty and BuildPlan returns
	// *MissingFactoryError for every step name.
	_ "github.com/plane-watcher/plane-feeder/internal/build/steps"
)

func main() {
	repoRoot, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "builder:", err)
		os.Exit(2)
	}
	if len(os.Args) > 1 && os.Args[1] == "sync-recipe" {
		os.Exit(runSyncRecipe(repoRoot, os.Args[2:], os.Stdout))
	}
	sel, interactive, err := parseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	cfg, err := config.LoadAll(config.LoadAllOptions{
		RepoRoot:    repoRoot,
		EnvExplicit: os.Getenv("CONFIG_FILE") != "",
		EnvPath:     os.Getenv("CONFIG_FILE"),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	if interactive {
		os.Exit(runTUI(cfg))
	}
	os.Exit(runCLI(cfg, sel))
}

// findRepoRoot ascends from CWD until it finds a .git directory or file
// (git worktrees use a .git file pointing at the main repo).
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not locate repo root (no .git from %s)", dir)
		}
		dir = parent
	}
}

// runCLI builds the plan, acquires the lock, runs the plan, prints
// events to stderr. Returns process exit code.
func runCLI(cfg *config.Config, sel build.Selections) int {
	plan, err := build.BuildPlan(sel, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "plan:", err)
		return 2
	}
	if len(plan.Steps) == 0 {
		fmt.Fprintln(os.Stderr, "no work selected")
		return 0
	}
	lock, err := build.AcquireLock(cfg.LockPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer lock.Release()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		cancel()
	}()

	runner := build.NewRunner(plan)
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	for e := range runner.Events {
		printCLIEvent(e)
	}
	if err := <-done; err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func printCLIEvent(e build.Event) {
	switch e.Kind {
	case build.EventStarted:
		fmt.Fprintf(os.Stderr, "[%s] started\n", e.Step)
	case build.EventLogLine:
		fmt.Fprintf(os.Stdout, "[%s] %s\n", e.Step, e.Payload)
	case build.EventProgress:
		if p, ok := e.Payload.(build.ProgressPayload); ok {
			fmt.Fprintf(os.Stderr, "[%s] %s\n", e.Step, p.Message)
		}
	case build.EventTimingResult:
		if r, ok := e.Payload.(build.TimingResult); ok {
			status := "met"
			if !r.MetConstraints {
				status = "NOT MET"
			}
			fmt.Fprintf(os.Stderr, "[%s] timing %s: WNS=%g TNS=%g WHS=%g\n", e.Step, status, r.WNS, r.TNS, r.WHS)
		}
	case build.EventWarning:
		fmt.Fprintf(os.Stderr, "[%s] WARN: %v\n", e.Step, e.Payload)
	case build.EventFinished:
		fmt.Fprintf(os.Stderr, "[%s] done\n", e.Step)
	case build.EventFailed:
		fmt.Fprintf(os.Stderr, "[%s] FAILED: %v\n", e.Step, e.Payload)
	case build.EventCancelled:
		fmt.Fprintf(os.Stderr, "[%s] cancelled\n", e.Step)
	case build.EventSkipped:
		fmt.Fprintf(os.Stderr, "[%s] skipped\n", e.Step)
	}
}

// runTUI lives in tui.go.
