package shell

import "testing"

func TestGetLoginCommand(t *testing.T) {
	testCases := [][]string{
		{"", "sudo login -f root"},
		{"action", "sudo login -f action"},
	}

	for _, tc := range testCases {
		cmd := GetLoginCommand(tc[0])
		if cmd != tc[1] {
			t.Errorf("Expect %s but got %s", tc[1], cmd)
		}
	}
}
