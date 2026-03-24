// Package gpiod1 provides a Go client for the libgpiod gpio-manager D-Bus API
// (io.gpiod1). It connects to a local or remote system D-Bus and wraps the
// io.gpiod1.Chip, io.gpiod1.Request, and io.gpiod1.Line interfaces.
//
// For most use cases, [NewController] is the simplest entry point:
//
//	ctrl, err := gpiod1.NewController("unix:abstract=/run/dbus-gpio")
//	defer ctrl.Close()
//	ctrl.Drive(17, true)
//	val, err := ctrl.Read(27)
//
// For lower-level access, use [Connect] directly:
//
//	client, err := gpiod1.Connect("tcp:host=192.168.1.42,port=50103")
//	chip, err := client.Chip("gpiochip0")
//	req, err := chip.RequestLines(gpiod1.LineConfig{
//	    Offsets:   []uint32{17, 27},
//	    Direction: "output",
//	}, gpiod1.RequestConfig{Consumer: "my-app"})
//	req.SetValues(map[uint32]bool{17: true, 27: false})
//	req.Release()
//	client.Close()
package gpiod1

import (
	"encoding/xml"
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"
)

const (
	busName      = "io.gpiod1"
	ifaceChip    = "io.gpiod1.Chip"
	ifaceRequest = "io.gpiod1.Request"
	rootPath     = "/io/gpiod1"
	chipsPath    = "/io/gpiod1/chips"
)

// Client holds a connection to the D-Bus and provides access to gpio-manager.
type Client struct {
	conn *dbus.Conn
}

// Connect connects to a D-Bus system bus. The address follows the D-Bus address
// specification (the same format as DBUS_SYSTEM_BUS_ADDRESS). Examples:
//
//	// Unix abstract socket:
//	gpiod1.Connect("unix:abstract=/tmp/dbus-gpio")
//
//	// Regular Unix socket:
//	gpiod1.Connect("unix:path=/var/run/dbus/system_bus_socket")
//
//	// TCP (requires dbus-daemon listening on TCP):
//	gpiod1.Connect("tcp:host=192.168.1.42,port=50103")
//
// Authentication is negotiated automatically: EXTERNAL for Unix sockets,
// ANONYMOUS for TCP.
func Connect(address string) (*Client, error) {
	opts := []dbus.ConnOption{}
	if len(address) >= 4 && address[:4] == "tcp:" {
		opts = append(opts, dbus.WithAuth(dbus.AuthAnonymous()))
	}
	conn, err := dbus.Connect(address, opts...)
	if err != nil {
		return nil, fmt.Errorf("gpiod1: connect: %w", err)
	}
	return &Client{conn: conn}, nil
}

// Close closes the D-Bus connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// introspectNode is used to parse D-Bus Introspect XML.
type introspectNode struct {
	Children []introspectNode `xml:"node"`
	Name     string           `xml:"name,attr"`
}

// Chips returns the names of all GPIO chips managed by gpio-manager.
func (c *Client) Chips() ([]string, error) {
	obj := c.conn.Object(busName, chipsPath)
	var xmlStr string
	if err := obj.Call("org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xmlStr); err != nil {
		return nil, fmt.Errorf("gpiod1: introspect chips: %w", err)
	}
	var node introspectNode
	if err := xml.Unmarshal([]byte(xmlStr), &node); err != nil {
		return nil, fmt.Errorf("gpiod1: parse introspect XML: %w", err)
	}
	var chips []string
	for _, child := range node.Children {
		if child.Name != "" {
			chips = append(chips, child.Name)
		}
	}
	return chips, nil
}

// Chip returns a handle for the named GPIO chip (e.g. "gpiochip0").
func (c *Client) Chip(name string) (*Chip, error) {
	path := dbus.ObjectPath(chipsPath + "/" + name)
	return &Chip{client: c, path: path, name: name}, nil
}

// ReleaseExistingRequests releases all active line requests on the D-Bus.
// This is called automatically by [NewController] on startup to clean up any
// requests left behind by a previous process that exited uncleanly.
//
// Note: this releases requests across all chips on the connection.
func (c *Client) ReleaseExistingRequests() {
	const requestsPath = rootPath + "/requests"
	obj := c.conn.Object(busName, requestsPath)
	var xmlStr string
	if err := obj.Call("org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xmlStr); err != nil {
		return
	}
	var node introspectNode
	if err := xml.Unmarshal([]byte(xmlStr), &node); err != nil {
		return
	}
	for _, child := range node.Children {
		if child.Name != "" {
			reqPath := dbus.ObjectPath(requestsPath + "/" + child.Name)
			r := &Request{client: c, path: reqPath}
			r.Release()
		}
	}
}

// --------------------------------------------------------------------------
// Chip
// --------------------------------------------------------------------------

// Chip represents a GPIO chip object on the D-Bus.
type Chip struct {
	client *Client
	path   dbus.ObjectPath
	name   string
}

// Name returns the kernel name of the chip (e.g. "gpiochip0").
func (ch *Chip) Name() string { return ch.name }

// Label returns the chip label (e.g. "pinctrl-bcm2711").
func (ch *Chip) Label() (string, error) {
	v, err := ch.client.conn.Object(busName, ch.path).GetProperty(ifaceChip + ".Label")
	if err != nil {
		return "", err
	}
	s, _ := v.Value().(string)
	return s, nil
}

// NumLines returns the number of GPIO lines on this chip.
func (ch *Chip) NumLines() (uint32, error) {
	v, err := ch.client.conn.Object(busName, ch.path).GetProperty(ifaceChip + ".NumLines")
	if err != nil {
		return 0, err
	}
	n, _ := v.Value().(uint32)
	return n, nil
}

// LineConfig describes how a group of GPIO lines should be configured.
type LineConfig struct {
	// Offsets are the GPIO line numbers to request (required).
	Offsets []uint32
	// Direction: "input" or "output".
	Direction string
	// OutputValues sets the initial output state per offset (output mode only).
	// Defaults to inactive (false) for any offset not listed.
	OutputValues map[uint32]bool
	// ActiveLow inverts the line polarity.
	ActiveLow bool
	// Bias: "pull-up", "pull-down", or "disabled" (default).
	Bias string
	// Drive: "push-pull" (default), "open-drain", or "open-source".
	Drive string
	// Edge detection (input only): "rising", "falling", or "both".
	Edge string
}

// RequestConfig contains request-level metadata.
type RequestConfig struct {
	// Consumer is a human-readable name for the requesting application.
	Consumer string
}

// RequestLines requests one or more GPIO lines on this chip and returns a
// [Request] handle for reading or writing them.
func (ch *Chip) RequestLines(cfg LineConfig, rcfg RequestConfig) (*Request, error) {
	if len(cfg.Offsets) == 0 {
		return nil, errors.New("gpiod1: at least one offset required")
	}

	obj := ch.client.conn.Object(busName, ch.path)

	type lineGroup struct {
		Offsets  []uint32
		Settings map[string]dbus.Variant
	}

	settings := map[string]dbus.Variant{}
	if cfg.Direction != "" {
		settings["direction"] = dbus.MakeVariant(cfg.Direction)
	}
	if cfg.ActiveLow {
		settings["active-low"] = dbus.MakeVariant(true)
	}
	if cfg.Bias != "" {
		settings["bias"] = dbus.MakeVariant(cfg.Bias)
	}
	if cfg.Drive != "" {
		settings["drive"] = dbus.MakeVariant(cfg.Drive)
	}
	if cfg.Edge != "" {
		settings["edge"] = dbus.MakeVariant(cfg.Edge)
	}

	groups := []lineGroup{{Offsets: cfg.Offsets, Settings: settings}}

	defaults := make([]int32, len(cfg.Offsets))
	for i, off := range cfg.Offsets {
		if v, ok := cfg.OutputValues[off]; ok && v {
			defaults[i] = 1
		}
	}

	lineConfig := struct {
		Groups   []lineGroup
		Defaults []int32
	}{groups, defaults}

	reqConfig := map[string]dbus.Variant{}
	if rcfg.Consumer != "" {
		reqConfig["consumer"] = dbus.MakeVariant(rcfg.Consumer)
	}

	var requestPath dbus.ObjectPath
	call := obj.Call(ifaceChip+".RequestLines", 0, lineConfig, reqConfig)
	if call.Err != nil {
		return nil, fmt.Errorf("gpiod1: RequestLines: %w", call.Err)
	}
	if err := call.Store(&requestPath); err != nil {
		return nil, fmt.Errorf("gpiod1: RequestLines store result: %w", err)
	}

	return &Request{
		client:  ch.client,
		path:    requestPath,
		offsets: cfg.Offsets,
	}, nil
}

// --------------------------------------------------------------------------
// Request
// --------------------------------------------------------------------------

// Request represents an active GPIO line request returned by [Chip.RequestLines].
type Request struct {
	client  *Client
	path    dbus.ObjectPath
	offsets []uint32
}

// Path returns the D-Bus object path of this request.
func (r *Request) Path() dbus.ObjectPath { return r.path }

// GetValues reads the current values of all lines in this request.
// Returns a map of offset → active (true = high for active-high lines).
func (r *Request) GetValues() (map[uint32]bool, error) {
	obj := r.client.conn.Object(busName, r.path)
	var raw []int32
	call := obj.Call(ifaceRequest+".GetValues", 0, r.offsets)
	if call.Err != nil {
		return nil, fmt.Errorf("gpiod1: GetValues: %w", call.Err)
	}
	if err := call.Store(&raw); err != nil {
		return nil, fmt.Errorf("gpiod1: GetValues store: %w", err)
	}
	out := make(map[uint32]bool, len(raw))
	for i, v := range raw {
		out[r.offsets[i]] = v != 0
	}
	return out, nil
}

// SetValues sets the output values of lines in this request.
// values maps offset → active state (true = high for active-high lines).
func (r *Request) SetValues(values map[uint32]bool) error {
	obj := r.client.conn.Object(busName, r.path)
	raw := make(map[uint32]int32, len(values))
	for k, v := range values {
		if v {
			raw[k] = 1
		} else {
			raw[k] = 0
		}
	}
	call := obj.Call(ifaceRequest+".SetValues", 0, raw)
	if call.Err != nil {
		return fmt.Errorf("gpiod1: SetValues: %w", call.Err)
	}
	return nil
}

// SetValue sets a single line. Convenience wrapper around [Request.SetValues].
func (r *Request) SetValue(offset uint32, active bool) error {
	return r.SetValues(map[uint32]bool{offset: active})
}

// GetValue reads a single line. Convenience wrapper around [Request.GetValues].
func (r *Request) GetValue(offset uint32) (bool, error) {
	vals, err := r.GetValues()
	if err != nil {
		return false, err
	}
	return vals[offset], nil
}

// Reconfigure changes the configuration of the lines in this request.
func (r *Request) Reconfigure(cfg LineConfig) error {
	obj := r.client.conn.Object(busName, r.path)
	type lineGroup struct {
		Offsets  []uint32
		Settings map[string]dbus.Variant
	}
	settings := map[string]dbus.Variant{}
	if cfg.Direction != "" {
		settings["direction"] = dbus.MakeVariant(cfg.Direction)
	}
	if cfg.ActiveLow {
		settings["active-low"] = dbus.MakeVariant(true)
	}
	if cfg.Bias != "" {
		settings["bias"] = dbus.MakeVariant(cfg.Bias)
	}
	if cfg.Drive != "" {
		settings["drive"] = dbus.MakeVariant(cfg.Drive)
	}
	if cfg.Edge != "" {
		settings["edge"] = dbus.MakeVariant(cfg.Edge)
	}
	groups := []lineGroup{{Offsets: cfg.Offsets, Settings: settings}}
	defaults := make([]int32, len(cfg.Offsets))
	lineConfig := struct {
		Groups   []lineGroup
		Defaults []int32
	}{groups, defaults}
	call := obj.Call(ifaceRequest+".ReconfigureLines", 0, lineConfig)
	if call.Err != nil {
		return fmt.Errorf("gpiod1: Reconfigure: %w", call.Err)
	}
	return nil
}

// Release releases the line request, freeing all lines back to the kernel.
func (r *Request) Release() error {
	obj := r.client.conn.Object(busName, r.path)
	call := obj.Call(ifaceRequest+".Release", 0)
	if call.Err != nil {
		return fmt.Errorf("gpiod1: Release: %w", call.Err)
	}
	return nil
}
