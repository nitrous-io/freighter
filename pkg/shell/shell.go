package shell

import (
	"errors"
	"fmt"
	"io"
	"log"
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
)

var (
	ErrMissingArg  = errors.New("Missing arguments: check id and command")
	ErrInvalidTerm = errors.New("Invalid term")

	// time to wait for process to close normally before we terminate it
	defaultFinishTimeout = 1 * time.Second
	//  time to wait for process to close before we kill it
	defaultTermTimeout = 2 * time.Second
)

func NewShell(id, command string, noTTY bool) (*Shell, error) {
	if id == "" || command == "" {
		return nil, ErrMissingArg
	}
	s := &Shell{
		id:      id,
		command: command,
		isTTY:   true,
	}
	if noTTY {
		s.isTTY = false
	}
	return s, nil
}

func OpenShell(id, command string, width, height int, noTTY bool, env []string) (*Shell, error) {
	s, err := NewShell(id, command, noTTY)
	if err != nil {
		return nil, err
	}
	if err := s.Open(width, height, env); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

type Shell struct {
	id      string
	command string
	term    *os.File
	// we use lxc-attach by default, but user can switch this to a more
	// lightweight command like bash for testing purpose
	cmd *exec.Cmd

	// if the shell requires a TTY
	isTTY bool

	stdinW  io.WriteCloser
	stdoutR io.ReadCloser
	stdoutW io.WriteCloser
}

func (s *Shell) Close() error {
	if s.cmd != nil && s.cmd.Process != nil {
		if s.isTTY {
			closeProcess(s.cmd)
		} else {
			s.cmd.Wait()
		}
	}
	if s.term != nil {
		return s.term.Close()
	}
	return nil
}

func (s *Shell) Open(w, h int, env []string) error {
	if w == 0 {
		w = defaultWidth
	}
	if h == 0 {
		h = defaultHeight
	}
	if s.cmd == nil {
		args := []string{
			"exec",
			"-it",
			s.id,
			"/bin/sh",
			"-c",
			fmt.Sprintf("TERM=%s %s", defaultTerm, s.command),
		}
		s.cmd = exec.Command("docker", args...)
	}
	log.Printf("[INFO] executing command %s", s.cmd)

	s.stdoutR, s.stdoutW = io.Pipe()
	if s.isTTY {
		sh, err := pty.Start(s.cmd)
		if err != nil {
			return err
		}
		s.term = sh

		// create stdin pipes for tty
		stdinR, stdinW := io.Pipe()
		s.stdinW = stdinW

		// copy output from terminal to stdin out
		go func() {
			defer s.stdoutW.Close()
			io.Copy(s.stdoutW, s.term)
		}()

		// when stdin is closed, close the stdout pipe as well. This prevent cases
		// where we hangs the shell waiting for stdout to finished while stdin is
		// closed
		promise(func() error {
			_, err := io.Copy(s.term, stdinR)
			// if the client closed the write part of the tcp connection, we gets
			// UnexpectedEOF from the websocket, so we ignore it here
			if err == io.ErrClosedPipe || err == io.ErrUnexpectedEOF {
				err = nil
			}
			return err
		})
	} else {
		// setup the cmd pipes without pty
		stdin, err := s.cmd.StdinPipe()
		if err != nil {
			return err
		}
		s.cmd.Stdout = s.stdoutW
		s.cmd.Stderr = s.stdoutW
		s.stdinW = stdin

		if err := s.cmd.Start(); err != nil {
			return err
		}
	}
	return s.Resize(w, h)
}

func (s *Shell) Attach(stdin io.Reader, stdout io.Writer) chan error {
	// setups the pipes so we can close the copy routine properly instead of
	// using the raw terminal
	go func() {
		io.Copy(s.stdinW, stdin)
		s.stdinW.Close()
		s.stdoutR.Close()
	}()

	// stdout
	finishStdout := promise(func() error {
		_, err := io.Copy(stdout, s.stdoutR)
		if err == io.ErrClosedPipe {
			err = nil
		}
		return err
	})

	return promise(func() error {
		defer s.Close()
		return <-finishStdout
	})
}

func (s *Shell) Resize(w, h int) error {
	// if this is not a tty we dont need to resize
	if !s.isTTY {
		return nil
	}
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
func closeProcess(c *exec.Cmd) error {
	done := make(chan error, 1)
	go func() {
		done <- c.Wait()
	}()
	select {
	case <-time.After(defaultFinishTimeout):
		c.Process.Signal(syscall.SIGTERM)
	case <-time.After(defaultTermTimeout):
		return c.Process.Kill()
	case err := <-done:
		return err
	}
	return nil
}
