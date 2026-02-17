package supervisor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"
)

type Config struct {
	Name       string
	BinaryPath string
	GRPCAddr   string
}

type Supervisor struct {
	cfg Config

	mu  sync.Mutex
	cmd *exec.Cmd
}

func New(cfg Config) *Supervisor {
	return &Supervisor{cfg: cfg}
}

func (s *Supervisor) EnsureRunning(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd != nil && s.cmd.Process != nil && s.cmd.ProcessState == nil {
		return nil
	}

	if _, err := os.Stat(s.cfg.BinaryPath); err != nil {
		return fmt.Errorf("adapter binary missing: %w", err)
	}

	cmd := exec.Command(s.cfg.BinaryPath, "--listen", s.cfg.GRPCAddr)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start adapter process: %w", err)
	}

	s.cmd = cmd
	prefix := s.cfg.Name
	if prefix == "" {
		prefix = "adapter"
	}
	go scan(stdout, prefix+":stdout")
	go scan(stderr, prefix+":stderr")
	go s.waitProcess(cmd)

	time.Sleep(250 * time.Millisecond)
	return nil
}

func (s *Supervisor) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	return s.cmd.Process.Kill()
}

func (s *Supervisor) waitProcess(cmd *exec.Cmd) {
	if err := cmd.Wait(); err != nil {
		log.Printf("adapter exited with error: %v", err)
	} else {
		log.Printf("adapter exited")
	}
}

func scan(r io.Reader, prefix string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		log.Printf("%s %s", prefix, scanner.Text())
	}
}
