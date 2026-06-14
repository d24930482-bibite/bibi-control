package ipc

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

type Process struct {
	cmd *exec.Cmd

	mu     sync.RWMutex
	info   ProcessInfo
	done   chan struct{}
	waitMu sync.Once
}

func StartProcess(ctx context.Context, spec ProcessSpec) (*Process, error) {
	if spec.Path == "" {
		return nil, fmt.Errorf("ipc: process path is required")
	}

	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = mergeEnv(os.Environ(), spec.Env)

	if spec.Stdout != nil {
		cmd.Stdout = spec.Stdout
	} else {
		cmd.Stdout = io.Discard
	}
	if spec.Stderr != nil {
		cmd.Stderr = spec.Stderr
	} else {
		cmd.Stderr = io.Discard
	}

	p := &Process{
		cmd:  cmd,
		done: make(chan struct{}),
		info: ProcessInfo{
			Path:  spec.Path,
			Args:  append([]string(nil), spec.Args...),
			Dir:   spec.Dir,
			State: ProcessStarting,
		},
	}

	if err := cmd.Start(); err != nil {
		p.info.State = ProcessFailed
		p.info.Error = err.Error()
		close(p.done)
		return nil, err
	}

	p.mu.Lock()
	p.info.PID = cmd.Process.Pid
	p.info.StartedAt = time.Now().UTC()
	p.info.State = ProcessRunning
	p.mu.Unlock()

	go p.wait()
	return p, nil
}

func (p *Process) PID() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.info.PID
}

func (p *Process) Info() ProcessInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := p.info
	out.Args = append([]string(nil), p.info.Args...)
	return out
}

func (p *Process) Done() <-chan struct{} { return p.done }

func (p *Process) Wait(ctx context.Context) (ProcessInfo, error) {
	select {
	case <-ctx.Done():
		return p.Info(), ctx.Err()
	case <-p.done:
		info := p.Info()
		if info.Error != "" {
			return info, fmt.Errorf("ipc: process exited with error: %s", info.Error)
		}
		return info, nil
	}
}

// Kill forcibly terminates the OS process. Graceful shutdown should be handled
// by the game-owned compatibility endpoint before calling Kill.
func (p *Process) Kill() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return ErrNoProcess
	}
	return p.cmd.Process.Kill()
}

func (p *Process) wait() {
	p.waitMu.Do(func() {
		err := p.cmd.Wait()
		now := time.Now().UTC()

		p.mu.Lock()
		defer p.mu.Unlock()
		defer close(p.done)

		p.info.ExitedAt = &now
		if p.cmd.ProcessState != nil {
			exitCode := p.cmd.ProcessState.ExitCode()
			p.info.ExitCode = &exitCode
		}

		if err != nil {
			p.info.State = ProcessFailed
			p.info.Error = err.Error()
			return
		}
		p.info.State = ProcessExited
	})
}

func mergeEnv(base []string, extra map[string]string) []string {
	out := append([]string(nil), base...)
	for k, v := range extra {
		out = append(out, k+"="+v)
	}
	return out
}
