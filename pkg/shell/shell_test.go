package shell

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"testing"
	"time"

	"github.com/nitrous-io/nitrous-common/term"
)

func testShell() *Shell {
	s := &Shell{
		id:       "localbox",
		username: "action",
		cmd:      exec.Command("bash"),
	}
	return s
}

func TestShellOpen(t *testing.T) {
	s := testShell()
	defer s.Close()
	err := s.Open("xterm-256color", 100, 80)
	if err != nil {
		t.Fatalf("Unexpected error %v", err)
	}
	ws, err := term.GetWinsize(s.fd())
	if err != nil || ws.Width != 100 || ws.Height != 80 {
		t.Fatalf("Unexpected size %v %v", ws, err)
	}
}

func TestShellAttach(t *testing.T) {
	s := testShell()
	defer s.Close()
	err := s.Open("xterm-256color", 100, 80)
	if err != nil {
		t.Fatalf("Unexpected error %v", err)
	}

	out := bytes.NewBuffer(nil)
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	go io.Copy(out, stdoutR)

	done := s.Attach(stdinR, stdoutW)

	go func() {
		fmt.Fprint(stdinW, "echo")
		<-time.After(200 * time.Millisecond)
		stdinR.CloseWithError(io.EOF)
		stdoutW.CloseWithError(io.EOF)
	}()

	select {
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out")
	case <-done:
		if c := out.String(); c != "echo" {
			t.Fatalf("Expect %v, got %v", "echo", c)
		}
	}
}

func TestShellResize(t *testing.T) {
	s := testShell()
	defer s.Close()
	err := s.Open("xterm-256color", 100, 80)
	if err != nil {
		t.Fatalf("Unexpected error %v", err)
	}
	ws, err := term.GetWinsize(s.fd())
	if err != nil || ws.Width != 100 || ws.Height != 80 {
		t.Fatalf("Unexpected size %v %v", ws, err)
	}

	err = s.Resize(80, 80)
	if err != nil {
		t.Fatalf("Unexpected error %v", err)
	}
	ws, err = term.GetWinsize(s.fd())
	if err != nil || ws.Width != 80 || ws.Height != 80 {
		t.Fatalf("Unexpected size %v %v", ws, err)
	}
}
