package main

import (
	"log"
	"time"

	"github.com/haydenroche5/gpiod1"
)

func main() {
	// Connect to the system D-Bus on a remote host, forwarded via socat:
	//   socat UNIX-LISTEN:/tmp/dbus-gpio,fork \
	//         UNIX-CONNECT:/var/run/dbus/system_bus_socket
	ctrl, err := gpiod1.NewController("unix:abstract=/tmp/dbus-gpio")
	if err != nil {
		log.Fatal(err)
	}
	defer ctrl.Close()

	// Drive GPIO 17 high for 5 s, then low for 5 s, 10 times.
	for i := 0; i < 10; i++ {
		if err := ctrl.Drive(17, true); err != nil {
			log.Fatal(err)
		}
		time.Sleep(5 * time.Second)

		if err := ctrl.Drive(17, false); err != nil {
			log.Fatal(err)
		}
		time.Sleep(5 * time.Second)
	}
}
