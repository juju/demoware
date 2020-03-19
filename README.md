# Demoware

A test service that simulates a poll-able metrics stream.

# Getting started

To start a server listening at port **8080** and requiring basic authentication 
run the following set of commands:

```console
$ make build
$ ./demoware --with-auth-token=deadbeef
```

You can then access the server via curl and pretty-print the output with the 
help of jq:

```console
$ curl deadbeef@localhost:8080/metrics | jq .
```

The Makefile also includes a `run` target which starts a server using the above
configuration options and additionally injects random errors with a 10%
probability.

# Options

To see the list of supported options, just run the following command:

```console
$ ./demoware -h
USAGE:
   demoware [global options] command [command options] [arguments...]

COMMANDS:
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --listen-address value          the address to listen for incoming API connections (default: ":8080")
   --listen-tls-key value          path to a file with a TLS cert for the server (enables TLS support)
   --listen-tls-password value     path to the TLS key for the server (enables TLS support)
   --metrics-endpoint value        endpoint for serving metrics requests (default: "/metrics")
   --metrics-min-count value       minimum number of metrics to return in responses (default: 0)
   --metrics-max-count value       maximum number of metrics to return in responses (default: 10)
   --with-auth-token value         if specified, require clients to provide basic auth token
   --with-random-error-prob value  if non-zero, inject errors based on the given probability (default: 0)
   --help, -h                      show help (default: false)
```

# Payload formats

The server from this package emits three distinct types of metrics. Each metric
has its own payload format (see below) but all metrics wrapped in an envelope
that adheres to the following format:

```json
{
	"type": "payload_type",
	"payload": {...}
}
```

The supported payload types and their respective payload descriptions are:

- **load_avg**: reports an observed load average for a machine. Here is an
example (including envelope) for this metric type:

```json
 {
    "type": "load_avg",
    "payload": {
      "value": 0.721704
    }
  }
```

- **cpu_usage**: reports the CPU usage for the machine CPU. Here is an
example (including envelope) for this metric type:

```json
{
    "type": "cpu_usage",
    "payload": {
      "value": [
        0.39495406,
        0.8537769,
        0.555475,
        0.7067777,
        0.2921814
      ]
    }
}
```

- **last_kernel_upgrade**: reports the time when the kernel was last upgraded.
Here is an example (including envelope) for this metric type:

```json
{
    "type": "last_kernel_upgrade",
    "payload": {
      "value": "2020-03-18T16:29:40.720121054Z"
    }
}
```
