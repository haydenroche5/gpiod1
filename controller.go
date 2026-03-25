package gpiod1

import (
	"errors"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var errControllerClosed = errors.New("gpiod1: controller is closed")

// Controller is a high-level GPIO controller that manages line requests
// automatically. Each GPIO offset gets its own request, created on first use
// and released when [Controller.Close] is called.
//
// Signal handling (SIGINT, SIGTERM) is installed automatically so that Ctrl+C
// and normal process termination release all GPIO lines cleanly.
type Controller struct {
	client   *Client
	chip     *Chip
	mu       sync.Mutex
	requests map[uint32]*lineRequest
	closed   bool
}

type lineRequest struct {
	req       *Request
	direction string // "input" or "output"
}

// NewController connects to the D-Bus at address and returns a Controller for
// the specified chip (default: "gpiochip0").
//
// Call [Controller.Close] when done (typically via defer).
func NewController(address string, chipName ...string) (*Controller, error) {
	name := "gpiochip0"
	if len(chipName) > 0 && chipName[0] != "" {
		name = chipName[0]
	}

	client, err := Connect(address)
	if err != nil {
		return nil, err
	}

	chip, err := client.Chip(name)
	if err != nil {
		client.Close()
		return nil, err
	}

	c := &Controller{
		client:   client,
		chip:     chip,
		requests: make(map[uint32]*lineRequest),
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		c.Close()
		os.Exit(0)
	}()

	return c, nil
}

// Drive sets GPIO line offset high (active=true) or low (active=false).
// The line is automatically configured as output on first use.
func (c *Controller) Drive(offset uint32, active bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return errControllerClosed
	}

	req, err := c.ensureOutput(offset, active)
	if err != nil {
		return err
	}
	return req.SetValue(offset, active)
}

// Read returns the current value of GPIO input line offset.
// The line is automatically configured as input on first use.
func (c *Controller) Read(offset uint32) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return false, errControllerClosed
	}

	req, err := c.ensureInput(offset)
	if err != nil {
		return false, err
	}
	return req.GetValue(offset)
}

// Float reconfigures a GPIO line to floating input (bias-disabled), handing
// control back to the external circuit without releasing the kernel handle.
// This is the correct way to de-assert a driven pin on hardware where
// Release alone leaves the output register latched (e.g. Raspberry Pi 5 RP1).
// The request remains alive so Close still cleans it up, and a subsequent
// Drive call can reconfigure without a release/re-request cycle.
func (c *Controller) Float(offset uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return errControllerClosed
	}

	lr, ok := c.requests[offset]
	if !ok {
		return nil
	}

	if err := lr.req.Reconfigure(LineConfig{
		Offsets:   []uint32{offset},
		Direction: "input",
		Bias:      "disabled",
	}); err != nil {
		return err
	}
	lr.direction = "input"
	return nil
}

// Pulse drives a GPIO line to active for duration then floats it.
// This is the correct pattern for toggling reset pins: it guarantees the
// external pull resistor can de-assert the line after the pulse regardless
// of what the hardware output register retains after a plain Release.
func (c *Controller) Pulse(offset uint32, active bool, duration time.Duration) error {
	if err := c.Drive(offset, active); err != nil {
		return err
	}
	time.Sleep(duration)
	return c.Float(offset)
}

// Release releases the kernel GPIO request for a single offset.
// Note: on some hardware (e.g. Raspberry Pi 5 RP1) the output register is
// latched after release, so the pin may not float. Use [Controller.Float]
// first if you need the external circuit to take over the line state.
// It is a no-op if the offset is not currently held.
func (c *Controller) Release(offset uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return errControllerClosed
	}

	if lr, ok := c.requests[offset]; ok {
		delete(c.requests, offset)
		return lr.req.Release()
	}
	return nil
}

// Close releases all GPIO line requests and closes the D-Bus connection.
// It is safe to call multiple times.
func (c *Controller) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true

	var first error
	for offset, lr := range c.requests {
		if err := lr.req.Release(); err != nil && first == nil {
			first = err
		}
		delete(c.requests, offset)
	}
	if err := c.client.Close(); err != nil && first == nil {
		first = err
	}
	return first
}

// ensureOutput returns a request for offset configured as output, creating or
// replacing it as needed. Must be called with c.mu held.
func (c *Controller) ensureOutput(offset uint32, active bool) (*Request, error) {
	if lr, ok := c.requests[offset]; ok {
		if lr.direction == "output" {
			return lr.req, nil
		}
		lr.req.Release()
		delete(c.requests, offset)
	}

	req, err := c.chip.RequestLines(
		LineConfig{
			Offsets:      []uint32{offset},
			Direction:    "output",
			OutputValues: map[uint32]bool{offset: active},
		},
		RequestConfig{Consumer: "gpio-controller"},
	)
	if err != nil {
		return nil, err
	}

	c.requests[offset] = &lineRequest{req: req, direction: "output"}
	return req, nil
}

// ensureInput returns a request for offset configured as input, creating or
// replacing it as needed. Must be called with c.mu held.
func (c *Controller) ensureInput(offset uint32) (*Request, error) {
	if lr, ok := c.requests[offset]; ok {
		if lr.direction == "input" {
			return lr.req, nil
		}
		lr.req.Release()
		delete(c.requests, offset)
	}

	req, err := c.chip.RequestLines(
		LineConfig{
			Offsets:   []uint32{offset},
			Direction: "input",
		},
		RequestConfig{Consumer: "gpio-controller"},
	)
	if err != nil {
		return nil, err
	}

	c.requests[offset] = &lineRequest{req: req, direction: "input"}
	return req, nil
}
