package shell

import (
	"errors"
	"io"
	"os"
	"os/exec"

	"github.com/kr/pty"
	"github.com/nitrous-io/nitrous-common/term"
)

var (
	ErrInvalidTerm = errors.New("Invalid term")
)

func NewShell(id, username, term string, width, height int) (*Shell, error) {
	s := &Shell{
		id:       id,
		username: username,
	}
	if err := s.Open(term, width, height); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

type Shell struct {
	username string
	id       string
	term     *os.File
	// we use lxc-attach by default, but user can switch this to a more
	// lightweight command like bash for testing purpose
	cmd *exec.Cmd
}

func (s *Shell) Close() error {
	return s.term.Close()
}

func (s *Shell) Open(term string, w, h int) error {
	if s.cmd == nil {
		args := []string{
			"--name", s.id,
			"--clear-env",
			"--set-var", "TERM=" + term,
			"--",
			"sudo", "login", "-f", s.username,
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
	return term.SetWinsize(s.fd(), &term.Winsize{
		Width:  uint16(w),
		Height: uint16(h),
	})
}

// get the file descriptor of the underlying shell.
func (s *Shell) fd() uintptr {
	return s.term.Fd()
}

// Go is a basic promise implementation: it wraps calls a function in a goroutine,
// and returns a channel which will later return the function's return value.
func promise(f func() error) chan error {
	ch := make(chan error, 1)
	go func() {
		ch <- f()
	}()
	return ch
}
