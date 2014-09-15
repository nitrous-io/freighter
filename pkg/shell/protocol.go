// this handles our websocket protocol
// server <--> client
// --> d|action@foobar $ # output from server
// <-- d|whoami          # input from client
// --> d|action          # output from server
// <-- r|80 25           # resize from client
// <-- c|good bye        # close msg
//
// NOT IMPLEMENTED
// <-- h                  # heartbeat from client
package shell

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

var (
	headerData      = []byte("d")
	headerResize    = []byte("r")
	headerClose     = []byte("c")
	headerHeartbeat = []byte("h")

	ErrUnexpectedData = errors.New("unexpected data received")
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
	width  int
	height int
}

func (m *ResizeMsg) String() string {
	return fmt.Sprintf("resize: w %d, h %d", m.width, m.height)
}

func NewResizeMsg(msg []byte) fmt.Stringer {
	in := bytes.NewBuffer(msg)
	var w, h int
	i, err := fmt.Fscanf(in, "%d %d", &w, &h)
	if err != nil || i != 2 {
		return &InvalidMsg{fmt.Sprintf("invalid resize msg %s", string(msg))}
	}
	return &ResizeMsg{width: w, height: h}
}

// CloseMsg represents the close msg that client sends before closing the
// connection
type CloseMsg struct {
	msg string
}

func (m *CloseMsg) String() string {
	return fmt.Sprintf("close: %s", m.msg)
}

type ShellReader struct {
	reader io.Reader
	header []byte
	buffer *bytes.Buffer
	// for out of band input such as heartbeat / resize / close
	msgChan chan Message
}

func NewShellReader(r io.Reader, ch chan Message) io.Reader {
	return &ShellReader{reader: r, msgChan: ch}
}

func (s *ShellReader) startBuffer(r io.Reader) ([]byte, error) {
	s.buffer = bytes.NewBuffer(nil)
	return s.fillBuffer(r)
}

func (s *ShellReader) fillBuffer(r io.Reader) ([]byte, error) {
	if s.buffer == nil {
		return nil, ErrUnexpectedData
	}
	rBuf := bytes.NewBuffer(nil)
	if _, err := rBuf.ReadFrom(r); err != nil {
		s.buffer = nil
		return nil, err
	}

	// read until ETX
	data, err := rBuf.ReadBytes('\x03')

	if err == nil { // ETX reached
		// strip ETX
		data = data[:len(data)-1]
		if _, err := s.buffer.Write(data); err != nil {
			s.buffer = nil
			return nil, err
		}
		return s.finishBuffer(), nil
	}

	// ETX not reached
	_, err = s.buffer.Write(data)
	if err != nil {
		s.buffer = nil
	}
	return nil, err
}

func (s *ShellReader) finishBuffer() []byte {
	// dump and decode base64
	data := s.buffer.String()
	s.buffer = nil
	decoded, _ := base64.StdEncoding.DecodeString(data)
	return decoded
}

func (s *ShellReader) Read(out []byte) (int, error) {
	input := make([]byte, len(out))
	n, err := s.reader.Read(input)

	if err != nil || n == 0 {
		return 0, err
	}

	inBuf := bytes.NewBuffer(input[:n])
	var data []byte

	switch input[0] {
	case '\x01': // SOH
		// read from SOH until STX
		header, err := inBuf.ReadBytes('\x02')
		if err != nil {
			return 0, err
		}
		// remove SOH and STX to get header
		s.header = header[1 : len(header)-1]

		// start reading payload
		data, err = s.startBuffer(inBuf)
		if err != nil {
			return 0, err
		}
	case '\x03': // ETX
		data = s.finishBuffer()
	default:
		// if buffering, append to existing buffer
		data, err = s.fillBuffer(inBuf)
		if err != nil {
			return 0, err
		}
	}

	if data == nil {
		return 0, nil
	}

	if bytes.Equal(s.header, headerData) { // if data
		i := copy(out, data)
		return i, nil
	} else if bytes.Equal(s.header, headerResize) { // if resize
		msg := NewResizeMsg(data)
		s.msgChan <- msg
		return 0, nil
	} else if bytes.Equal(s.header, headerClose) { // if close
		msg := &CloseMsg{string(data)}
		s.msgChan <- msg
		return 0, nil
	}

	return 0, nil
}

// ShellWriter handles server write which outputs
type ShellWriter struct {
	writer io.Writer
}

func NewShellWriter(w io.Writer) io.Writer {
	return &ShellWriter{writer: w}
}

func (s *ShellWriter) Write(data []byte) (int, error) {
	dataLen := len(data)
	base64DataLen := base64.StdEncoding.EncodedLen(dataLen)
	buf := make([]byte, 1+len(headerData)+1+base64DataLen+1) // <SOH><event name><STX><base64-data><ETX>

	i := 0
	buf[0] = '\x01' // SOH
	i += 1
	copy(buf[i:], headerData)
	i += len(headerData)
	buf[i] = '\x02' // STX
	i += 1
	base64.StdEncoding.Encode(buf[i:], data)
	i += base64DataLen
	buf[i] = '\x03' // ETX

	_, err := s.writer.Write(buf)
	return dataLen, err
}
