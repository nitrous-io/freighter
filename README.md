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

name=foo&image=ubuntu-upstart:trusty&callback_url=http://example.com/cb&public_key[]=ssh-rsa%20AAAAB3NzaC1yc2EAAAADAQABAAABAQCzRcsDraIFmeuZ%2Bq6Ic9epAzXPy8WnkqZwLnauH9YBMPhrVQH6nnr%2FYSGxhYivnG1Qu0AirSUBAjFL7uD9YLeUhwwdKkhHa%2BbPPMpycmOopyafZyYAEIuZUwyjnK%2F2mniNJZ%2BYZhfBJ1z9Xh1hMIFtls%2FLP1Q7C25rIDg7l1khZ1cEV0mAzJjb4QBvqAt6TjC%2FYbUZ4%2F45rHRuQsu%2FRBhYpuffFwqlLHfjSO2i6TuJtNsEJ2kDDLooM%2FTHf3J2TxObomusErPTgwAo8ya1GmEB5NuwysZx1posjPZIQwg0Q3RqLzMOPlnZpG2l%2FlvVfEU%2BiCcc246z4D4Ej9WnuWFR%20jihoon%40ubuntu&state=xyz
```

**Example response**:

```javascript
HTTP/1.1 202 Accepted
Content-Type: application/json; charset=utf-8

{ "status": "accepted" }
```

**POST Params**:

- **name** - container name (a-z, 0-9, and hyphen(-), can't start with hyphen or digits, can't end with hyphen.)
- **image** - base image to use
- **public\_key[]** - _(optional)_ ssh pub keys to add
- **callback\_url** - _(optional)_ callback url to which freighter will POST result data
- **state** - _(optional)_ an opaque value used by the client to maintain state between the request and callback. If given, freighter will include this value in the callback

**Status Codes**:

- **202** - accepted
- **422** - missing or invalid params

**Example callback (success)**:

```javascript
{
  "result": "created",
  "container": {
    "id":  "2cf1503ed6a0",
    "name": "foobar",
    "image": "freighter.foobar",
    "host": "1.2.3.4",
    "ssh_port": 10000,
    "state": "running"
  },
  "state": "xyz"
}
```

**Example callback (failure)**:

```javascript
{ "error": "pc load letter", "state": "xyz" }
```

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

callback_url=http://example.com/cb&state=xyz
```

**Example response**:

```javascript
HTTP/1.1 202 Accepted
Content-Type: application/json; charset=utf-8

{ "status": "accepted" }
```

**POST Params**:

- **callback\_url** - _(optional)_ callback url to which freighter will POST result data
- **state** - _(optional)_ an opaque value used by the client to maintain state between the request and callback. If given, freighter will include this value in the callback.

**Status Codes**:

- **202** - accepted
- **404** - no such container

**Example callback (success)**:

```javascript
{
  "result": "started",
  "container": {
    "id": "2cf1503ed6a0",
    "name": "foobar",
    "image": "freighter.foobar",
    "host": "1.2.3.4",
    "ssh_port": 10000,
    "state": "running"
  },
  "state": "xyz"
}
```

**Example callback (failure)**:

```javascript
{ "error": "pc load letter", "state": "xyz" }
```

### Stop a container

`POST /containers/(id)/stop`

**Example request**:

```
POST /containers/2cf1503ed6a0/stop HTTP/1.1
Content-Type: application/x-www-form-urlencoded

callback_url=http://example.com/cb&state=xyz
```

**Example response**:

```javascript
HTTP/1.1 202 Accepted
Content-Type: application/json; charset=utf-8

{ "status": "accepted" }
```

**POST Params**:

- **callback\_url** - _(optional)_ callback url to which freighter will POST result data
- **state** - _(optional)_ an opaque value used by the client to maintain state between the request and callback. If given, freighter will include this value in the callback.

**Status Codes**:

- **202** - accepted
- **404** - no such container

**Example callback (success)**:

```javascript
{
  "result": "stopped",
  "container": {
    "id": "2cf1503ed6a0",
    "name": "foobar",
    "image": "freighter.foobar",
    "host": "",
    "ssh_port": 10000,
    "state": "stopped"
  },
  "state": "xyz"
}
```

**Example callback (failure)**:

```javascript
{ "error": "pc load letter", "state": "xyz" }
```

### Restart a container

`POST /containers/(id)/restart`

**Example request**:

```
POST /containers/2cf1503ed6a0/restart HTTP/1.1
Content-Type: application/x-www-form-urlencoded

callback_url=http://example.com/cb&state=xyz
```

**Example response**:

```javascript
HTTP/1.1 202 Accepted
Content-Type: application/json; charset=utf-8

{ "status": "accepted" }
```

**POST Params**:

- **callback\_url** - _(optional)_ callback url to which freighter will POST result data
- **state** - _(optional)_ an opaque value used by the client to maintain state between the request and callback. If given, freighter will include this value in the callback.

**Status Codes**:

- **202** - accepted
- **404** - no such container

**Example callback (success)**:

```javascript
{
  "result": "restarted",
  "container": {
    "id": "2cf1503ed6a0",
    "name": "foobar",
    "image": "freighter.foobar",
    "host": "1.2.3.4",
    "ssh_port": 10000,
    "state": "running"
  },
  "state": "xyz"
}
```

**Example callback (failure)**:

```javascript
{ "error": "pc load letter", "state": "xyz" }
```

### Terminate a container

`DELETE /containers/(id)`

**Example request**:

```
DELETE /containers/2cf1503ed6a0?callback_url=http://example.com/cb&state=xyz HTTP/1.1
```

**Example response**:

```javascript
HTTP/1.1 202 Accepted
Content-Type: application/json; charset=utf-8

{ "status": "accepted" }
```

**Query Params**:

- **callback\_url** - _(optional)_ callback url to which freighter will POST result data
- **state** - _(optional)_ an opaque value used by the client to maintain state between the request and callback. If given, freighter will include this value in the callback.

**Status Codes**:

- **202** - accepted
- **404** - no such container

**Example callback (success)**:

```javascript
{
  "result": "terminated",
  "container": {
    "id":  "2cf1503ed6a0"
  },
  "state": "xyz"
}
```

**Example callback (failure)**:

```javascript
{ "error": "pc load letter", "state": "xyz" }
```

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

### Run a command in the container

This should be used when we want to run the command in a non-interactive
environment and want to get back a multiplexed stream of stdout/stderr

`POST /containers/(id)/exec`

**Example request**:

```
POST /containers/2cf1503ed6a0/exec HTTP/1.1
```

**Query Params**:

- **command** - command to run in the container.
- **username** - _(optional)_ username to run the command. Default to root

**Example response**:

```javascript
HTTP/1.1 200 OK
Content-Type: application/vnd.freighterd.raw-stream

{{ STREAM }}
```

**Status Codes**:

- **200** - no error
- **404** - no such container

- - -
Copyright (c) 2014 Irrational Industries, Inc. d.b.a. Nitrous.IO. All Rights Reserved.
