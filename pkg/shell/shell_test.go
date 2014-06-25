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

// writer that sends all data written to it into a channel
type chanWriter struct {
	ch chan []byte
	w  io.Writer
}

func (cw *chanWriter) Write(p []byte) (int, error) {
	cw.ch <- p
	return cw.w.Write(p)
}

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

	written := make(chan []byte)
	go s.Attach(stdinR, &chanWriter{ch: written, w: stdoutW})

	go func() {
		fmt.Fprint(stdinW, "echo")
	}()

	select {
	case <-time.After(1000 * time.Millisecond):
		t.Fatal("timed out")
	case data := <-written:
		if string(data) != "echo" {
			t.Fatalf("Unexpected data %v", string(data))
		}
	}
}

func TestShellResize(t *testing.T) {
	s := testShell()
	defer s.Close()
	if err := s.Open(100, 80); err != nil {
		t.Fatalf("Unexpected error %v", err)
	}

	testCases := []struct {
		w, ew int
		h, eh int
	}{
		{w: 80, h: 80, ew: 80, eh: 80},
		{ew: defaultWidth, eh: defaultHeight},
	}

	for _, tc := range testCases {
		if err := s.Resize(tc.w, tc.h); err != nil {
			t.Fatalf("Unexpected error %v", err)
		}
		ws, err := term.GetWinsize(s.fd())
		if err != nil || int(ws.Width) != tc.ew || int(ws.Height) != tc.eh {
			t.Fatalf("Unexpected size %v %v with test %+v", ws, err, tc)
		}
	}
}
