freighter
=========

[![Build Status](https://ci-use1.nitrous.io/buildStatus/icon?job=freighter)](https://ci-use1.nitrous.io/job/freighter/)

## Development

* `vagrant up` using the bundled `Vagrantfile`
* Run `make test` to run tests
* Run `make run` to run server

* freighter folder is bind-mounted inside the docker container, so you
  can make edits outside, and run tests inside.

The server can be accessed from your Vagrant VM via `127.0.0.1:65333`, and from
other Vagrant VMs by the IP address given in the `FREIGHTER_INTERFACE`
environment variable (passed to Vagrant on `vagrant up`), for example
`33.33.33.12:65333`.

## REST API

### Login to a docker registry

`POST /registry/auth`

**Example request**:

```
POST /registry/auth HTTP/1.1
Content-Type: application/x-www-form-urlencoded

username=daniel&password=anything&email=daniel@nitrous.io&registry=private.registry.io
```

**Example response**:

```javascript
HTTP/1.1 201 Created
Content-Type: application/json; charset=utf-8

{ "status": "created" }
```

**POST Params**:

- **username** - username for the registry
- **password** - password for the registry
- **email** - email for the registry
- **registry** - _(optional)_ custom registry server, if not specified we will
  use the docker default registry

**Status Codes**:

- **201** - accepted
- **422** - missing or invalid params

### Logout of a docker registry

`DELETE /registry/auth`

**Example request**:

```
DELETE /registry/auth?registry=private.registry.io
```

**Example response**:

```javascript
HTTP/1.1 204 No Content
Content-Type: application/json; charset=utf-8
```

**DELETE Params**:

- **registry** - _(optional)_ custom registry server, if not specified we will
  use the docker default registry

**Status Codes**:

- **204** - no content
- **422** - missing or invalid params

### List containers

`GET /containers`

**Example request**:

```
GET /containers HTTP/1.1
```

**Example response**:

```javascript
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{
  "containers": [
    {
      "id":  "2cf1503ed6a0",
      "name": "foobar",
      "image": "freighter.foobar",
      "host": "1.2.3.4",
      "ssh_port": 10000,
      "state": "running"
    },
    {
      "id": "bbbbbbbbbbbb",
      "name": "bar",
      "image": "freighter.bar",
      "host": "1.2.3.5",
      "ssh_port": 10001,
      "state": "stopped"
    }
  ]
}
```

**Status Codes**:

- **200**  - no error

### Create a container

`POST /containers`

**Example request**:

```
POST /containers HTTP/1.1
Content-Type: application/x-www-form-urlencoded

name=foo&image=ubuntu-upstart:trusty&config=JSON
```

**Example response**:

```javascript
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{ "container" : { "id":"aaa", "name":"fragrant-wind-69", "image":"ubuntu", "host":"1.2.3.4", "ssh_port":10000, "state":"running", "ports":{"3000": ["3000", "3001"],"8080":["49123"],"22":["49273"]} }
```

**POST Params**:

- **name** - container name (a-z, 0-9, and hyphen(-), can't start with hyphen or digits, can't end with hyphen.)
- **image** - base image to use
- **config** - optional configuration json to passthrough to Docker

All keys of config JSON are optional.  Fully populated JSON looks like:
```
{
  "Config": {
    "Env": ["LANG=C"],
    "ExposedPorts": {"8080/tcp": {}, "3000/tcp": {}},
    "Volumes": {"/data": {}, "/music": {}}
  }
  "HostConfig": {
    "PortBindings": {"3000/tcp": [{"HostPort":"3000", "HostIp": "127.0.0.1"}]},
    "PortBindings": {"3000/tcp": [{"HostPort":"3001", "HostIp": "127.0.0.1"}]},
  }
}
```


**Status Codes**:

- **200** - ok
- **422** - missing or invalid params

### Get container info

`GET /containers/(id)`

**Example request**:

```
GET /containers/2cf1503ed6a0 HTTP/1.1
```

**Example response**:

```javascript
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{
  "container": {
    "id":"2cf1503ed6a0",
    "name":"foobar",
    "image":"freighter.foobar",
    "host":"1.2.3.4",
    "ssh_port":60003,
    "state":"running"
  }
}
```

**Status codes**:

- **200** - no error
- **404** - no such container

### Start a container

`POST /containers/(id)/start`

**Example request**:

```
POST /containers/2cf1503ed6a0/start HTTP/1.1
Content-Type: application/x-www-form-urlencoded

config=JSON
```

**Example response**:

```javascript
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{
	"result": "started",
	"container": {
		"id": "1001",
		"name": "container-name",
		"image": "nitrousio/ubuntu",
		"host": "1.2.3.4",
		"ssh_port": 1000,
		"state": "running",
		"ports": {"3000":["3000", "3001"],"22":["49273"]}
	}
}
```

**POST Params**:

- **config** - optional configuration json to passthrough to Docker

All keys of config JSON are optional.  If "Config" key is passed, it will be
ignored.  A fully populated JSON looks like:

```
{
  "HostConfig": {
    "PortBindings": {"3000/tcp": [{"HostPort":"1234", "HostIp": "127.0.0.1"}]},
  }
}
```

**Status Codes**:

- **200** - ok
- **404** - no such container

### Stop a container

`POST /containers/(id)/stop`

**Example request**:

```
POST /containers/2cf1503ed6a0/stop HTTP/1.1
Content-Type: application/x-www-form-urlencoded
```

**Example response**:

```javascript
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{
	"result": "stopped",
	"container": {
		"id": "1001",
		"name": "container-name",
		"image": "nitrousio/ubuntu",
		"host": "1.2.3.4",
		"ssh_port": 1000,
		"state": "stopped"
	}
}
```

**POST Params**:

**Status Codes**:

- **200** - ok
- **404** - no such container

### Restart a container

`POST /containers/(id)/restart`

**Example request**:

```
POST /containers/2cf1503ed6a0/restart HTTP/1.1
Content-Type: application/x-www-form-urlencoded
```

**Example response**:

```javascript
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{
  "result": "restarted",
  "container": {
    "id": "2cf1503ed6a0",
    "name": "foobar",
    "image": "freighter.foobar",
    "host": "1.2.3.4",
    "ssh_port": 10000,
    "state": "running"
  }
}
```

**POST Params**:

**Status Codes**:

- **200** - ok
- **404** - no such container

**Example callback (success)**:

### Terminate a container

`DELETE /containers/(id)`

**Example request**:

```
DELETE /containers/2cf1503ed6a0
```

**Example response**:

```javascript
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{
  "result": "terminated",
  "container": {
    "id":  "2cf1503ed6a0"
  }
}
```

**Query Params**:

**Status Codes**:

- **200** - ok
- **404** - no such container

### List authorized public keys for a container

`GET /containers/(id)/public_keys`

**Example request**:

```
GET /containers/2cf1503ed6a0/public_keys HTTP/1.1
```

**Example response**:

```javascript
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{
  "public_keys": {
    "c1:fc:6d:29:ab:b8:03:57:c4:30:a6:b7:58:40:74:af": "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCzRcsDraIFmeuZ+q6Ic9epAzXPy8WnkqZwLnauH9YBMPhrVQH6nnr/YSGxhYivnG1Qu0AirSUBAjFL7uD9YLeUhwwdKkhHa+bPPMpycmOopyafZyYAEIuZUwyjnK/2mniNJZ+YZhfBJ1z9Xh1hMIFtls/LP1Q7C25rIDg7l1khZ1cEV0mAzJjb4QBvqAt6TjC/YbUZ4/45rHRuQsu/RBhYpuffFwqlLHfjSO2i6TuJtNsEJ2kDDLooM/THf3J2TxObomusErPTgwAo8ya1GmEB5NuwysZx1posjPZIQwg0Q3RqLzMOPlnZpG2l/lvVfEU+iCcc246z4D4Ej9WnuWFR jihoon@ubuntu",
    "e1:82:d8:93:44:20:cc:ff:c1:b8:0c:c3:82:bc:17:a9": "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQDMEj71VsgCsMiV3yRhAoot2vozfg2tFklv53MchMxjoQEebXCbdevKs92d9R+9c7hPG4h8uiTwozfC6IMYLNXQgQsUItmoWZqu16a6iQ5j7vmkkNPNR78moJIr/8ZQkg4K2I0382QVSU1JB3Sp2QrsTrwU46PtKiiASTJPTP5ynCpVECTwZKdcf6LfA9GXTpEgJJQkKRFrkGAJxMeZDRI4PBR5UDYGCOivIvGB7DQCLuEQax0T6M+FmMISU7UqXZ03947w0muunLhwMVPC76GI0OpYB+4hRmXjLv/LYDRES3rhB2T2fuq4iCXQ3SzI1o5bG1FPBoWprCBYWvbzHnjb jihoon@mac"
  }
}
```

**Status Codes**:

- **200**  - no error
- **404** - no such container

### Add authorized public keys

`POST /containers/(id)/public_keys`

**Example request**:

```
POST /containers/2cf1503ed6a0/public_keys HTTP/1.1
Content-Type: application/x-www-form-urlencoded

public_key[]=ssh-rsa%20AAAAB3NzaC1yc2EAAAADAQABAAABAQCzRcsDraIFmeuZ%2Bq6Ic9epAzXPy8WnkqZwLnauH9YBMPhrVQH6nnr%2FYSGxhYivnG1Qu0AirSUBAjFL7uD9YLeUhwwdKkhHa%2BbPPMpycmOopyafZyYAEIuZUwyjnK%2F2mniNJZ%2BYZhfBJ1z9Xh1hMIFtls%2FLP1Q7C25rIDg7l1khZ1cEV0mAzJjb4QBvqAt6TjC%2FYbUZ4%2F45rHRuQsu%2FRBhYpuffFwqlLHfjSO2i6TuJtNsEJ2kDDLooM%2FTHf3J2TxObomusErPTgwAo8ya1GmEB5NuwysZx1posjPZIQwg0Q3RqLzMOPlnZpG2l%2FlvVfEU%2BiCcc246z4D4Ej9WnuWFR%20jihoon%40ubuntu&public_key[]=ssh-rsa%20AAAAB3NzaC1yc2EAAAADAQABAAABAQDMEj71VsgCsMiV3yRhAoot2vozfg2tFklv53MchMxjoQEebXCbdevKs92d9R%2B9c7hPG4h8uiTwozfC6IMYLNXQgQsUItmoWZqu16a6iQ5j7vmkkNPNR78moJIr%2F8ZQkg4K2I0382QVSU1JB3Sp2QrsTrwU46PtKiiASTJPTP5ynCpVECTwZKdcf6LfA9GXTpEgJJQkKRFrkGAJxMeZDRI4PBR5UDYGCOivIvGB7DQCLuEQax0T6M%2BFmMISU7UqXZ03947w0muunLhwMVPC76GI0OpYB%2B4hRmXjLv%2FLYDRES3rhB2T2fuq4iCXQ3SzI1o5bG1FPBoWprCBYWvbzHnjb%20jihoon%40mac
```

**Example response**:

```javascript
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{
  "public_keys": {
    "c1:fc:6d:29:ab:b8:03:57:c4:30:a6:b7:58:40:74:af": "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCzRcsDraIFmeuZ+q6Ic9epAzXPy8WnkqZwLnauH9YBMPhrVQH6nnr/YSGxhYivnG1Qu0AirSUBAjFL7uD9YLeUhwwdKkhHa+bPPMpycmOopyafZyYAEIuZUwyjnK/2mniNJZ+YZhfBJ1z9Xh1hMIFtls/LP1Q7C25rIDg7l1khZ1cEV0mAzJjb4QBvqAt6TjC/YbUZ4/45rHRuQsu/RBhYpuffFwqlLHfjSO2i6TuJtNsEJ2kDDLooM/THf3J2TxObomusErPTgwAo8ya1GmEB5NuwysZx1posjPZIQwg0Q3RqLzMOPlnZpG2l/lvVfEU+iCcc246z4D4Ej9WnuWFR jihoon@ubuntu",
    "e1:82:d8:93:44:20:cc:ff:c1:b8:0c:c3:82:bc:17:a9": "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQDMEj71VsgCsMiV3yRhAoot2vozfg2tFklv53MchMxjoQEebXCbdevKs92d9R+9c7hPG4h8uiTwozfC6IMYLNXQgQsUItmoWZqu16a6iQ5j7vmkkNPNR78moJIr/8ZQkg4K2I0382QVSU1JB3Sp2QrsTrwU46PtKiiASTJPTP5ynCpVECTwZKdcf6LfA9GXTpEgJJQkKRFrkGAJxMeZDRI4PBR5UDYGCOivIvGB7DQCLuEQax0T6M+FmMISU7UqXZ03947w0muunLhwMVPC76GI0OpYB+4hRmXjLv/LYDRES3rhB2T2fuq4iCXQ3SzI1o5bG1FPBoWprCBYWvbzHnjb jihoon@mac"
  }
}
```

**POST Params**:

- **public\_key[]** - ssh pub keys to add

**Status Codes**:

- **200** - no error
- **422** - missing or invalid params
- **404** - no such container

### Remove an authorized public key

`DELETE /containers/(id)/public_keys/(key_fingerprint)`

**Example request**:

```
DELETE /containers/2cf1503ed6a0/public_keys/e1:82:d8:93:44:20:cc:ff:c1:b8:0c:c3:82:bc:17:a9 HTTP/1.1
```

**Example response**:

```javascript
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{
  "public_keys": {
    "c1:fc:6d:29:ab:b8:03:57:c4:30:a6:b7:58:40:74:af": "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCzRcsDraIFmeuZ+q6Ic9epAzXPy8WnkqZwLnauH9YBMPhrVQH6nnr/YSGxhYivnG1Qu0AirSUBAjFL7uD9YLeUhwwdKkhHa+bPPMpycmOopyafZyYAEIuZUwyjnK/2mniNJZ+YZhfBJ1z9Xh1hMIFtls/LP1Q7C25rIDg7l1khZ1cEV0mAzJjb4QBvqAt6TjC/YbUZ4/45rHRuQsu/RBhYpuffFwqlLHfjSO2i6TuJtNsEJ2kDDLooM/THf3J2TxObomusErPTgwAo8ya1GmEB5NuwysZx1posjPZIQwg0Q3RqLzMOPlnZpG2l/lvVfEU+iCcc246z4D4Ej9WnuWFR jihoon@ubuntu"
  }
}
```

**Status Codes**:

- **200** - no error
- **404** - no such container

### Export a container
`GET /containers/(id)/export`

**Example request**:

```
GET /containers/2cf1503ed6a0/export HTTP/1.1
```

**Example response**:

```javascript
HTTP/1.1 200 OK
Content-Type: application/octet-stream

{{ STREAM }}
```

The stream will be a [tar
archive](https://docs.docker.com/reference/api/docker_remote_api_v1.13/#21-containers)
Search for "Export a container"

**Status Codes**:

- **200** - no error
- **404** - no such container

### Print logs of a container

`GET /containers/(id)/logs`

**Example request**:

```
GET /containers/2cf1503ed6a0/logs HTTP/1.1
```

**Example response**:

```javascript
HTTP/1.1 200 OK
Content-Type: application/vnd.docker.raw-stream

{{ STREAM }}
```

**GET Params**:
- **follow**: follow logs
- **timestamps**: print timestamps
- **tail**: Output specified number of lines at the end of logs: `all` or `<number>`. Default all

**Status Codes**:

- **200** - no error
- **404** - no such container

### Run a command on the container

`POST /containers/(id)/run`

**Example request**:

```
 POST /containers/2cf1503ed6a0/run HTTP/1.1
Content-Type: application/x-www-form-urlencoded

command="whoami"&username=action
```

**Example response**:
When success, this establish a HTTP stream that user can write to /read from
the command just as if the user is interacting with it inside the container

```
Content-Type: application/octet-stream

{{ STREAM }}
```

**POST Params**:

- **command** - command that we want to run in the container
- **username** - (optional) user that we want to run the command. Default to
  root

**Status Codes**:

- **202** - accepted
- **404** - no such container


### Create a shell session on the container

`POST /containers/(id)/shells`

**Example request**:

```
POST /containers/2cf1503ed6a0/shells HTTP/1.1
```

**Query Params**:

- **command** - command to run in the shell.
- **width** - _(optional)_ initial width of the term. Default to 80
- **height** - _(optional)_ initial height of the term. Default to 60

**Example response**:

```javascript
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{
  "id": "135ab8202534d4ab40fb67e677e67863a7879440"
}
```

**Status Codes**:

- **200** - no error
- **404** - no such container

### Attach to a shell session

`POST /shells/(id)/attach`

**Example request**:

```
POST /shells/135ab8202534d4ab40fb67e677e67863a7879440/shells HTTP/1.1
```

**Example response**:

When success, this establish a HTTP stream that user can write to /read from
just as if the user is interacting with the shell stdin/stdout


**Status Codes**:

- **200** - no error
- **404** - no such shell session

### Resize a shell session

`POST /shells/(id)/resize`

**Example request**:

```
POST /shells/135ab8202534d4ab40fb67e677e67863a7879440/resize HTTP/1.1
Content-Type: application/x-www-form-urlencoded

width=100&height=80
```

**Example response**:

```
{ "status": "accepted" }
```

**Status Codes**:

- **202** - accepted
- **404** - no such shell session

### Listing freighter managed images on a host

```
GET /images HTTP/1.1
```

**Example response**:

```javascript
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8
{
  "images": [
    {
      "id": "cd9c7e7c1c7722201543137a2a3b09496bc1f885f39d222b8b731762ca580133",
      "name": "jpetazzo/busybox",
      "tag": "",
      "created_at": 1406054592,
      "actual_size": 333282,
      "virtual_size": 323787052
    },
    {
      "id": "fed6222a047ca3b027fbe7b53301f473aa03d9d53444a50c405dd2f087b1765b",
      "name": "nitrousio/ubuntu",
      "tag": "trusty",
      "created_at": 1406054685,
      "actual_size": 838,
      "virtual_size": 699414781
    }
  ]
}
```

### List volumes

`GET /hosts/(host slug)/volumes`

**Example request**:

`GET /hosts/myhost/volumes HTTP/1.1`

**Example response**:

```javascript
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{
  "volumes": [
    "dotfiles",
    "code",
    "db"
  ]
}
```

**Status Codes**:

- **200** - ok

### Create a volume

`POST /hosts/(host slug)/volumes`

**Example request**:

```
POST /hosts/myhost/volumes HTTP/1.1
Content-Type: application/x-www-form-urlencoded

slug=music
```

**Example response**:

```javascript
HTTP/1.1 201 Created
Content-Type: application/json; charset=utf-8

{ "status" : "created" }
```

**POST Params**:

- **slug** - required volume slug (alphanumeric, hyphen(-), underscore(\_), must start with alphanumeric).  This will be the directory name of the created volume

**Status Codes**:

- **201** - created
- **422** - missing or invalid params

### Remove a volume

`DELETE /hosts/(host slug)/volumes/(volume slug)`

**Example request**:

```
DELETE /hosts/myhost/volumes/oldvolume HTTP/1.1
Content-Type: application/json
```

**Example response**:

```javascript
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{ "status" : "ok" }
```

**Status Codes**:

- **200** - ok
- **422** - missing or invalid params

### Get volume info

`GET /hosts/(host slug)/volumes/(volume slug)`

**Example request**:

```
GET /hosts/myhost/volumes/sharedvol HTTP/1.1
```

**Example response**:

```javascript
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{
  "volume": {
    "slug":"sharedvol",
    "size":5915182,
  }
}
```

**Status codes**:

- **200** - ok
- **404** - no such container


- - -
Copyright (c) 2014 Irrational Industries, Inc. d.b.a. Nitrous.IO. All Rights Reserved.
