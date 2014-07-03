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
      "state": "running"
    },
    {
      "id": "bbbbbbbbbbbb",
      "name": "bar",
      "image": "freighter.bar",
      "host": "1.2.3.5",
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

- - -
Copyright (c) 2014 Irrational Industries, Inc. d.b.a. Nitrous.IO. All Rights Reserved.
