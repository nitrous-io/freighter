/*
By default, pkg shell logs user into a lxc-compatible container by running
lxc-attach on that container and then provide stdin/stdout stream to lxc-attach
process

User should can get the shell by

	s, _ := NewShell(containerId, "root")
	s.Open(80, 24)
	s.Attach(stdin, stdout)
	// works with stdin, stdout
	s.Resize(100, 60)
	s.Close()
*/
package shell
