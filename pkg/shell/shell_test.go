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

func TestNewShell(t *testing.T) {
	testCases := []struct {
		id, expectedId             string
		username, expectedUsername string
		expectedError              bool
	}{
		{id: "", username: "action", expectedError: true},
		{id: "1234", expectedId: "1234", username: "action", expectedUsername: "action"},
		{id: "1234", expectedId: "1234", expectedUsername: defaultUsername},
	}
	for _, tc := range testCases {
		s, err := NewShell(tc.id, tc.username)
		switch {
		case tc.expectedError && err != nil:
			continue
		case tc.expectedError && err == nil:
			t.Fatalf("Expected err but pass %+v", tc)
		case tc.expectedId != s.id || tc.expectedUsername != s.username:
			t.Fatalf("Incorrect id or username %+v %+v", tc, s)
		}
	}
}

func TestShellOpen(t *testing.T) {
	testCases := []struct {
		w, ew int
		h, eh int
	}{
		{ew: defaultWidth, eh: defaultHeight},
		{w: 1024, ew: 1024},
		{h: 1024, eh: 1024},
	}

	for _, tc := range testCases {
		s := testShell()
		if err := s.Open(tc.w, tc.h); err != nil {
			t.Fatalf("Unexpected error %v", err)
		}
		ws, err := term.GetWinsize(s.fd())
		if err != nil {
			t.Fatalf("Unexpected error %v", err)
		}
		if tc.ew > 0 && tc.ew != int(ws.Width) {
			t.Fatalf("Incorrect term width %+v %+v", tc, ws)
		}
		if tc.eh > 0 && tc.eh != int(ws.Height) {
			t.Fatalf("Incorrect term height %+v %+v", tc, ws)
		}
		s.Close()
	}
}

func TestShellAttach(t *testing.T) {
	s := testShell()
	defer s.Close()
	if err := s.Open(100, 80); err != nil {
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
	if err := s.Open(100, 80); err != nil {
		t.Fatalf("Unexpected error %v", err)
	}
	ws, err := term.GetWinsize(s.fd())
	if err != nil || ws.Width != 100 || ws.Height != 80 {
		t.Fatalf("Unexpected size %v %v", ws, err)
	}

	if err = s.Resize(80, 80); err != nil {
		t.Fatalf("Unexpected error %v", err)
	}
	ws, err = term.GetWinsize(s.fd())
	if err != nil || ws.Width != 80 || ws.Height != 80 {
		t.Fatalf("Unexpected size %v %v", ws, err)
	}
}
