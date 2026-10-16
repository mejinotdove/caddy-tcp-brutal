# Caddy TCP Brutal listener wrapper

This Caddy module enables the Linux `brutal` TCP congestion-control algorithm on TCP connections accepted by a Caddy listener. See [tcp-brutal](https://github.com/apernet/tcp-brutal) for the kernel module and installation instructions.

## Requirements and limitations

Install and load tcp-brutal on Linux before starting Caddy. Follow the [upstream installation instructions](https://github.com/apernet/tcp-brutal#install); v1 supports Linux 5.8 or newer, while v2 features such as groups require Linux 5.10 or newer. Older kernels may require additional work to install tcp-brutal. This wrapper configures local TCP send behavior. It does not configure inbound traffic and does not apply to HTTP/3 or QUIC.

With the default `group_id` of zero, the rate is bytes per second **for each TCP connection**. For example, `25000000` is 25 MB/s (200 Mbit/s) per connection, so multiple active connections can use an aggregate rate above that value. A nonzero `group_id` applies the configured rate to the shared group total. The kernel module and its availability are prerequisites; the Caddy module does not install or load the kernel module for you.

## Build

```sh
go test ./...
go test -race ./...
go vet ./...
GOOS=linux GOARCH=amd64 go build ./...
GOOS=linux GOARCH=arm64 go build ./...
GOOS=linux GOARCH=386 go build ./...
GOOS=linux GOARCH=arm go build ./...
```

Or build Caddy with the module:

```sh
go install github.com/caddyserver/xcaddy@latest
xcaddy build --with github.com/mejinotdove/caddy-tcp-brutal
```

To build against the current local checkout instead of fetching the published module, run this from the repository root:

```sh
xcaddy build v2.10.2 --with github.com/mejinotdove/caddy-tcp-brutal=.
```

## Configuration

The sample [caddy.json](caddy.json) keeps the admin endpoint on `127.0.0.1:8081` and serves HTTP on `:8080`. For an HTTPS server, place `tcp_brutal` before the explicit `tls` listener wrapper, as shown here:

```json
{
  "admin": { "listen": "127.0.0.1:8081" },
  "apps": {
    "http": {
      "servers": {
        "srv0": {
          "listen": [":443"],
          "listener_wrappers": [
            {
              "wrapper": "tcp_brutal",
              "enabled": true,
              "rate_bytes_per_second": 25000000,
              "cwnd_gain": 20,
              "failure_mode": "fallback"
            },
            { "wrapper": "tls" }
          ],
          "routes": [
            { "match": [{ "host": ["example.com"] }], "handle": [{ "handler": "file_server", "root": "/var/www" }] }
          ]
        }
      }
    }
  }
}
```

Replace `example.com` with a hostname that resolves to this server so Caddy can obtain a public certificate. The wrapper also unwraps existing TLS connections internally, so configurations where TLS is already applied before this wrapper can use the underlying TCP socket while returning the original TLS connection to Caddy.

### Caddyfile

The checked-in [Caddyfile](Caddyfile) configures the wrapper through Caddy's global `servers` option and keeps `tcp_brutal` before `tls` in `listener_wrappers`. A bare `tcp_brutal` enables the default configuration. To set options, add a block; the wrapper is enabled by default when present, and `enabled false` disables it:

```caddyfile
{
	servers {
		listener_wrappers {
			tcp_brutal {
				enabled true
				rate_bytes_per_second 25000000
				cwnd_gain 20
				group_id 0
				failure_mode fallback
			}
			tls
		}
	}
}

example.com {
	root * /var/www
	file_server
}
```

The subdirectives are `enabled`, `rate_bytes_per_second`, legacy `rate_bps`, `cwnd_gain`, `group_id`, and `failure_mode`. Rate fields use unsigned decimal bytes per second; Caddyfile parsing checks syntax and configuration values but does not probe the kernel. Format, adapt, validate, and run the sample with:

```sh
caddy fmt --overwrite Caddyfile
caddy adapt --config Caddyfile --adapter caddyfile --pretty
caddy validate --config Caddyfile --adapter caddyfile
caddy run --config Caddyfile --adapter caddyfile
```

`rate_bytes_per_second` is the preferred rate field. Existing configurations using `rate_bps` remain supported; despite its name, that field is also measured in bytes per second. If both fields are nonzero, they must have the same value; either field may be zero or omitted while the other supplies the rate. The default rate is `25000000` bytes/s. Allowed values are `62500` through `125000000000` bytes/s. `cwnd_gain` defaults to `20` and accepts values from 5 through 80.

`failure_mode` is `fallback` by default. Startup probes a temporary TCP socket using the configured rate and gain. In fallback mode, a failed probe emits one warning and bypasses tcp-brutal for this listener until Caddy is reloaded. In `strict` mode, a failed probe prevents Caddy from starting. At runtime, fallback returns a connection only if setup made no change or restored its previous congestion algorithm; it closes the connection if rollback cannot safely restore it. Strict mode also closes a failed connection and keeps accepting until a connection succeeds or the listener itself errors. Runtime failures are logged through Caddy's sampled logger. Successful per-connection setup is not logged.

`group_id` is optional and defaults to zero (no shared group). A nonzero group requires tcp-brutal v2 or newer; startup checks `TCP_BRUTAL_VERSION` without applying the group to the probe socket. Members of a group share its configured rate within the same UID and network namespace. Configuring any member changes the shared group, so coordinate its value across listeners.

Disable the wrapper with `"enabled": false` to bypass probing and socket setup.
