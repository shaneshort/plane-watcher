package steps

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/build"
)

type RunOptions struct {
	Argv []string          // command and args
	Dir  string            // working directory; empty = caller's CWD
	Env  []string          // env vars; nil = inherit
	Emit func(build.Event) // line-by-line stdout/stderr emitter
}

type ExitError struct {
	Code int
	Cmd  string
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("%s exited with status %d", e.Cmd, e.Code)
}

// Run executes Argv with stdout/stderr streamed line-by-line via Emit as
// EventLogLine (payload: string). Process is started in its own process
// group; ctx cancellation sends SIGTERM to the whole group, then SIGKILL
// after 5s.
func Run(ctx context.Context, opts RunOptions) error {
	if len(opts.Argv) == 0 {
		return errors.New("Run: empty argv")
	}
	cmd := exec.Command(opts.Argv[0], opts.Argv[1:]...)
	cmd.Dir = opts.Dir
	cmd.Env = opts.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	var wg sync.WaitGroup
	pump := func(r io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			opts.Emit(build.Event{Kind: build.EventLogLine, Payload: sc.Text()})
		}
	}
	wg.Add(2)
	go pump(stdout)
	go pump(stderr)

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	select {
	case <-ctx.Done():
		if cmd.Process != nil {
			pgid, _ := syscall.Getpgid(cmd.Process.Pid)
			if pgid > 0 {
				_ = syscall.Kill(-pgid, syscall.SIGTERM)
			} else {
				_ = cmd.Process.Signal(syscall.SIGTERM)
			}
			select {
			case <-waitErr:
			case <-time.After(5 * time.Second):
				if pgid > 0 {
					_ = syscall.Kill(-pgid, syscall.SIGKILL)
				} else {
					_ = cmd.Process.Kill()
				}
				<-waitErr
			}
		}
		wg.Wait()
		return ctx.Err()
	case err := <-waitErr:
		wg.Wait()
		if err == nil {
			return nil
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return &ExitError{Code: ee.ExitCode(), Cmd: opts.Argv[0]}
		}
		return err
	}
}
