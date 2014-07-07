package shell

import "fmt"

const (
	defaultUsername = "root"
)

func GetLoginCommand(username string) string {
	if username == "" {
		username = defaultUsername
	}
	return fmt.Sprintf("sudo login -f %s", username)
}
