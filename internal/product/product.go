package product

import (
	"fmt"
	"os"
	"strconv"
)

const Name = "Iris"

const Version = "1.10.0"

const RepoURL = "https://github.com/alplix/iris"

// DefaultGUIRPCPort is where the Iris client listens. It is deliberately not
// BOINC's port, so both clients can run on one machine.
const DefaultGUIRPCPort = 31418

// BOINCGUIRPCPort is the stock BOINC client's GUI RPC port; the Iris client
// never binds it.
const BOINCGUIRPCPort = 31416

// GUIRPCPort is the port the local client listens on and the manager talks to:
// IRIS_GUI_RPC_PORT when it is set to a valid port, otherwise the default.
// BOINC's own port is never used. note explains why a setting was ignored.
func GUIRPCPort() (port int, note string) {
	v := os.Getenv("IRIS_GUI_RPC_PORT")
	if v == "" {
		return DefaultGUIRPCPort, ""
	}
	p, err := strconv.Atoi(v)
	switch {
	case err != nil || p <= 0 || p >= 65536:
		return DefaultGUIRPCPort, fmt.Sprintf("invalid IRIS_GUI_RPC_PORT, using %d", DefaultGUIRPCPort)
	case p == BOINCGUIRPCPort:
		return DefaultGUIRPCPort, fmt.Sprintf("port %d belongs to the BOINC client, using %d", p, DefaultGUIRPCPort)
	}
	return p, ""
}

func UserAgent() string {
	return Name + "/" + Version
}
