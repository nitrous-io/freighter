freighter
=========

## Development

* `vagrant up` using the bundled `Vagrantfile`
* Run `script/run-shell` to start a shell in Docker

**In Docker shell:**
* Run `script/setup`
* Run `make test` to run tests
* Run `make run` to run server

* freighter folder is bind-mounted inside the docker container, so you
  can make edits outside, and run tests inside.

The server can be accessed from your Vagrant VM via `127.0.0.1:65333`, and from
other Vagrant VMs by the IP address given in the `FREIGHTER_INTERFACE`
environment variable (passed to Vagrant on `vagrant up`), for example
`33.33.33.12:65333`.

## REST API

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

nameufoo&image=ubuntu-upstart:trusty
```

**Example response**:

```javascript
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{ "container" : { "id":"aaa", "name":"fragrant-wind-69", "image":"ubuntu", "host":"1.2.3.4", "ssh_port":10000, "state":"running" } }
```

**POST Params**:

- **name** - container name (a-z, 0-9, and hyphen(-), can't start with hyphen or digits, can't end with hyphen.)
- **image** - base image to use

**Status Codes**:

- **200** - accepted
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
		"state": "running"
	}
}
```

**POST Params**:

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

{ "status": "accepted" }
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

- - -
Copyright (c) 2014 Irrational Industries, Inc. d.b.a. Nitrous.IO. All Rights Reserved.
