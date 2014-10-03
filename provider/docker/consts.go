package docker

const dockerfileUbuntuTmpl = `
FROM {{.Image}}
MAINTAINER Nitrous.IO, hello@nitrous.io

# compatible with images based on ubuntu-upstart

# Create action user and give sudo
RUN \
	useradd --create-home -s /bin/bash action && \
	adduser action sudo && \
	mkdir -p /etc/sudoers.d && \
	echo %action ALL=NOPASSWD:ALL > /etc/sudoers.d/action && \
	chmod 0440 /etc/sudoers.d/action

# Install essential tools
RUN \
	apt-get install -yq ssh sudo && \
	apt-get clean

# Regenerate SSH host key && generate ssh keypair
RUN \
	mkdir -p /var/run/sshd && \
	rm -rf /etc/ssh/ssh_host_* && \
	ssh-keygen -A && \
	mkdir /home/action/.ssh && \
	chmod 0700 /home/action/.ssh && \
	ssh-keygen -b 2048 -t rsa -N '' -C 'action@{{.Name}}' -f /home/action/.ssh/id_rsa && \
	chown -R action:action /home/action/.ssh

# Enable system-level authorized ssh keys
ADD sshd_config /etc/ssh/sshd_config
`

var sshdConfig = []byte(`# WARNING: EDITING THIS FILE MAY RENDER THIS CONTAINER INACCESSIBLE!!!
Port 22
Protocol 2
HostKey /etc/ssh/ssh_host_rsa_key
HostKey /etc/ssh/ssh_host_dsa_key
HostKey /etc/ssh/ssh_host_ecdsa_key
HostKey /etc/ssh/ssh_host_ed25519_key
UsePrivilegeSeparation yes
KeyRegenerationInterval 3600
ServerKeyBits 1024
SyslogFacility AUTH
LogLevel INFO
LoginGraceTime 120
PermitRootLogin yes
StrictModes yes
RSAAuthentication yes
PubkeyAuthentication yes
AuthorizedKeysFile /nitrous/ssh/authorized_keys %h/.ssh/authorized_keys
IgnoreRhosts yes
RhostsRSAAuthentication no
HostbasedAuthentication no
PermitEmptyPasswords no
ChallengeResponseAuthentication no
PasswordAuthentication no
X11Forwarding yes
X11DisplayOffset 10
PrintMotd no
PrintLastLog yes
TCPKeepAlive yes
AcceptEnv LANG LC_*
Subsystem sftp /usr/lib/openssh/sftp-server
UsePAM yes
`)
