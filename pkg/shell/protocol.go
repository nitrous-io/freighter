// this handles our websocket protocol
// server <--> client
// --> d|action@foobar $ # output from server
// <-- d|whoami          # input from client
// --> d|action          # output from server
// <-- r|80 25           # resize from client
// <-- c|good bye        # close msg from client
// --> c|                # close msg from server
//
// NOT IMPLEMENTED
// <-- h                  # heartbeat from client
package shell

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"unicode/utf8"
)

var (
	dataMsg      = []byte("d|")
	resizeMsg    = []byte("r|")
	closeMsg     = []byte("c|")
	heartbeatMsg = []byte("h")

	InvalidMessage = errors.New("invalid message")
)

type Message fmt.Stringer

// InvalidMsg is returned whenever the client sends a message with a invalid
// protocol that we dont recognize
type InvalidMsg struct {
	msg string
}

func (m *InvalidMsg) String() string { return m.msg }

// ResizeMsg implements Stringer interfaces, and contains the desirable width and
// height. This will be write to a channel that handles non input/output message
//
// Refers to ShellReader for more information
type ResizeMsg struct {
	Width  int
	Height int
}

func (m *ResizeMsg) String() string {
	return fmt.Sprintf("resize: w %d, h %d", m.Width, m.Height)
}

func NewResizeMsg(msg []byte) fmt.Stringer {
	in := bytes.NewBuffer(msg)
	var w, h int
	i, err := fmt.Fscanf(in, "%d %d", &w, &h)
	if err != nil || i != 2 {
		return &InvalidMsg{fmt.Sprintf("invalid resize msg %s", string(msg))}
	}
	return &ResizeMsg{Width: w, Height: h}
}

// CloseMsg represents the close msg that client sends before closing the
// connection
type CloseMsg struct {
	msg string
}

func (m *CloseMsg) String() string {
	return fmt.Sprintf("close: %s", m.msg)
}

type websocketReader interface {
	NextReader() (messageType int, r io.Reader, err error)
}

// ShellReader handles client input which allows type:
// - d|
// - r|
// - c|
// - h
type ShellReader struct {
	socket websocketReader

	// cached reader, which is reset whenever the messages is EOF
	reader io.Reader
	// for out of band input such as heartbeat / resize / close
	msgChan chan Message

	// message type for the protocol
	MessageType int
}

func NewShellReader(ws websocketReader, ch chan Message, t int) io.Reader {
	return &ShellReader{socket: ws, msgChan: ch, MessageType: t}
}

func (s *ShellReader) Read(p []byte) (int, error) {
	var newMessage bool
	// if we didnt have a reader, this is a new message and we should get a
	// reader for it
	if s.reader == nil {
		t, r, err := s.socket.NextReader()
		if err != nil {
			return 0, err
		}
		if t != s.MessageType {
			log.Printf("wsconn: ignored unknown message type. Got %d, wanted %d", t, s.MessageType)
			return 0, nil
		}
		s.reader = r
		newMessage = true
	}
	l := len(p)
	buf := make([]byte, l)
	n, err := s.reader.Read(buf)
	if err != nil {
		// When we have the frame EOF, treat it as no-op so we can receive the
		// next frame normally
		if err == io.EOF {
			s.reader = nil
			err = nil
		}
		return 0, err
	}
	// we ignore the heartbeat msg from the client
	if newMessage && n == 1 && bytes.Equal(buf[0:1], heartbeatMsg) {
		return 0, nil
	}
	// if this is a new message, we takes out the protocol prefix
	if newMessage && n > 2 {
		// detect client input, copy everything back to p without the prefix
		if bytes.Equal(buf[0:2], dataMsg) {
			i := copy(p, buf[2:n])
			return i, err
		}
		// detect resize msg
		if bytes.Equal(buf[0:2], resizeMsg) {
			msg := NewResizeMsg(buf[2:n])
			s.msgChan <- msg
			return 0, nil
		}
		// detect close msg
		if bytes.Equal(buf[0:2], closeMsg) {
			msg := &CloseMsg{string(buf[2:n])}
			s.msgChan <- msg
			return 0, nil
		}
	}
	// if this is not a new message, this is a fragment message and we just
	// copy it over without caring about the prefix
	if !newMessage {
		copy(p, buf)
		return n, err
	}
	return 0, nil
}

// ShellWriter takes data written to it and writes it as a data message of the
// form "d|server_output". It writes server output to be sent to the client.
type ShellWriter struct {
	writer io.Writer
}

// NewShellWriter creates a new ShellWriter that satisfies io.WriteCloser.
// It is the caller's responsibility to call Close on the WriteCloser when done.
func NewShellWriter(w io.Writer) *ShellWriter {
	return &ShellWriter{writer: w}
}

func (s *ShellWriter) Write(p []byte) (int, error) {
	l := len(p)
	buf := make([]byte, l+len(dataMsg))
	copy(buf[0:2], dataMsg)
	copy(buf[2:], p)
	if !utf8.Valid(buf) {
		stripped := make([]rune, 0, len(buf))
		for i := range buf {
			r, size := utf8.DecodeRune(buf[i:])
			if r == utf8.RuneError && size == 1 {
				continue
			}
			stripped = append(stripped, r)
		}
		buf = []byte(string(stripped))
	}
	_, err := s.writer.Write(buf)
	return l, err
}

// Close sends a close message to the client, but does not close the underlying
// io.Writer.
func (s *ShellWriter) Close() error {
	_, err := s.writer.Write(closeMsg)
	return err
}
