package shell

import (
	"bytes"
	"encoding/base64"
	"io"
	"reflect"
	"testing"
	"time"
)

func b64(str string) string {
	return base64.StdEncoding.EncodeToString([]byte(str))
}

func TestNewShellReader(t *testing.T) {
	fragmentedData := "fragmented freaking data"
	fragmentedDataB64 := b64(fragmentedData)

	testCases := []struct {
		msgs        []string
		expected    string
		expectedMsg Message
	}{
		{[]string{"\x01d\x02" + b64("whoami") + "\x03"}, "whoami", nil},
		{[]string{"\x01d\x02" + b64("ls") + "\x03"}, "ls", nil},
		{[]string{"\x01r\x02" + b64("80 20") + "\x03"}, "", &ResizeMsg{width: 80, height: 20}},
		{[]string{"\x01c\x02" + b64("good bye") + "\x03"}, "", &CloseMsg{msg: "good bye"}},
		{
			[]string{
				"\x01d\x02" + fragmentedDataB64[:8],
				fragmentedDataB64[8:15],
				fragmentedDataB64[15:] + "\x03",
			},
			fragmentedData,
			nil,
		},
	}

	for _, tc := range testCases {
		ch := make(chan Message, 1)
		r, w := io.Pipe()

		shellReader := NewShellReader(r, ch)
		out := bytes.NewBuffer(nil)

		var n int64
		done := make(chan struct{})
		go func() {
			n, _ = io.Copy(out, shellReader)
			done <- struct{}{}
		}()

		for _, msg := range tc.msgs {
			w.Write([]byte(msg))
		}
		r.Close()
		w.Close()
		<-done

		if n != int64(len(tc.expected)) {
			t.Errorf("expect to read %d bytes but got %d bytes", len(tc.expected), n)
		}

		if tc.expectedMsg != nil {
			select {
			case msg := <-ch:
				if !reflect.DeepEqual(msg, tc.expectedMsg) {
					t.Errorf("expect msg %v but got %v", tc.expectedMsg, msg)
				}
			case <-time.After(10 * time.Millisecond):
				t.Errorf("timed out while waiting for %v", tc.expectedMsg)
			}
		}

		if out.String() != tc.expected {
			t.Errorf("expect output %s but got %s", tc.expected, out.String())
		}
	}
}

func TestNewShellWriter(t *testing.T) {
	testCases := []struct {
		msg      string
		expected string
	}{
		{"action@foobar", "\x01d\x02" + b64("action@foobar") + "\x03"},
	}

	for _, tc := range testCases {
		w := bytes.NewBuffer(nil)
		s := NewShellWriter(w)

		r := bytes.NewBufferString(tc.msg)
		io.Copy(s, r)

		if w.String() != tc.expected {
			t.Errorf("expect %s to be written but got %s", tc.expected, w.String())
		}
	}
}
