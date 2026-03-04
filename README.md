# gpiod1

A Go client for the [libgpiod](https://libgpiod.readthedocs.io/) gpio-manager D-Bus API (`io.gpiod1`).

gpio-manager is the reference daemon for controlling GPIO lines on Linux via D-Bus. This library lets you talk to it from Go — locally or remotely over a forwarded socket.

## Requirements

The target host must be running `gpio-manager` from libgpiod 2.x with D-Bus support enabled.

## Installation

```sh
go get github.com/haydenroche5/gpiod1
```

## Usage

### High-level API

`Controller` handles all request lifecycle management automatically. GPIO lines are claimed on first use and released on `Close()`. SIGINT and SIGTERM are caught automatically so Ctrl+C cleans up properly.

```go
ctrl, err := gpiod1.NewController("unix:abstract=/tmp/dbus-gpio")
if err != nil {
    log.Fatal(err)
}
defer ctrl.Close()

// Drive an output line
ctrl.Drive(17, true)   // high
ctrl.Drive(17, false)  // low
ctrl.Release(17)       // float (release the line)

// Read an input line
val, err := ctrl.Read(27)
```

By default `gpiochip0` is used. To specify a different chip:

```go
ctrl, err := gpiod1.NewController(address, "gpiochip1")
```

### Low-level API

For direct access to chips and requests:

```go
client, err := gpiod1.Connect("unix:abstract=/tmp/dbus-gpio")
if err != nil {
    log.Fatal(err)
}
defer client.Close()

chip, _ := client.Chip("gpiochip0")

req, err := chip.RequestLines(
    gpiod1.LineConfig{
        Offsets:      []uint32{17},
        Direction:    "output",
        OutputValues: map[uint32]bool{17: false},
    },
    gpiod1.RequestConfig{Consumer: "my-app"},
)
if err != nil {
    log.Fatal(err)
}
defer req.Release()

req.SetValue(17, true)
val, _ := req.GetValue(17)
```

## Connecting to a Remote Host

The D-Bus socket can be forwarded from a remote machine using `socat`:

```sh
# On the remote host:
socat UNIX-LISTEN:/tmp/dbus-gpio,fork \
      UNIX-CONNECT:/var/run/dbus/system_bus_socket
```

Then connect using the local forwarded socket path.

## D-Bus Address Formats

| Format | Example |
|--------|---------|
| Unix abstract socket | `unix:abstract=/tmp/dbus-gpio` |
| Unix socket path | `unix:path=/var/run/dbus/system_bus_socket` |
| TCP | `tcp:host=192.168.1.42,port=50103` |

## License

MIT
