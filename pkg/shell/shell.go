package shell

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/kr/pty"
	"github.com/nitrous-io/nitrous-common/term"
)

const (
	defaultTerm   = "xterm-256color"
	defaultWidth  = 80
	defaultHeight = 60

	defaultTimeout = 100 * time.Millisecond
)

var (
	ErrMissingArg  = errors.New("Missing arguments: check id and command")
	ErrInvalidTerm = errors.New("Invalid term")
)

func NewShell(id, command string) (*Shell, error) {
	if id == "" || command == "" {
		return nil, ErrMissingArg
	}
	s := &Shell{
		id:      id,
		command: command,
	}
	return s, nil
}

func OpenShell(id, command string, width, height int) (*Shell, error) {
	s, err := NewShell(id, command)
	if err != nil {
		return nil, err
	}
	if err := s.Open(width, height); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

type Shell struct {
	username string
	id       string
	term     *os.File
	command  string
	// we use lxc-attach by default, but user can switch this to a more
	// lightweight command like bash for testing purpose
	cmd *exec.Cmd
}

func (s *Shell) Close() error {
	if s.cmd.Process != nil {
		go closeProcess(s.cmd, defaultTimeout)
	}
	return s.term.Close()
}

func (s *Shell) Open(w, h int) error {
	if w == 0 {
		w = defaultWidth
	}
	if h == 0 {
		h = defaultHeight
	}
	if s.cmd == nil {
		args := []string{
			"--name", s.id,
			"--clear-env",
			"--set-var", "TERM=" + defaultTerm,
			"--",
			"sh", "-c", s.command,
		}
		s.cmd = exec.Command("lxc-attach", args...)
	}
	sh, err := pty.Start(s.cmd)
	if err != nil {
		return err
	}
	s.term = sh
	return s.Resize(w, h)
}

func (s *Shell) Attach(stdin io.Reader, stdout io.Writer) chan error {
	errors := make(chan error, 2)

	// stdin
	go func() {
		_, err := io.Copy(s.term, stdin)
		if err == io.ErrClosedPipe {
			err = nil
		}
		errors <- err
	}()

	// stdout
	go func() {
		_, err := io.Copy(stdout, s.term)
		if err == io.ErrClosedPipe {
			err = nil
		}
		errors <- err
	}()

	return promise(func() error {
		defer s.Close()
		for i := 0; i < 2; i += 1 {
			if err := <-errors; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Shell) Resize(w, h int) error {
	if w == 0 {
		w = defaultWidth
	}
	if h == 0 {
		h = defaultHeight
	}
	return term.SetWinsize(s.fd(), &term.Winsize{
		Width:  uint16(w),
		Height: uint16(h),
	})
}

// get the file descriptor of the underlying shell.
func (s *Shell) fd() uintptr {
	return s.term.Fd()
}

// basic promise implementation: it wraps calls a function in a goroutine,
// and returns a channel which will later return the function's return value.
func promise(f func() error) chan error {
	ch := make(chan error, 1)
	go func() {
		ch <- f()
	}()
	return ch
}

// close the process by sending it SIGTERM and wait for it to complete.
// If it cant finish after timeout, kill it
func closeProcess(c *exec.Cmd, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() {
		done <- c.Wait()
	}()
	c.Process.Signal(syscall.SIGTERM)
	select {
	case <-time.After(timeout):
		return c.Process.Kill()
	case err := <-done:
		return err
	}
}
