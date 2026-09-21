package product

const Name = "Iris"

const Version = "1.0.2"

const RepoURL = "https://github.com/alplix/iris"

// DefaultGUIRPCPort is where the Iris client listens. It is deliberately not
// BOINC's port, so both clients can run on one machine.
const DefaultGUIRPCPort = 31418

// BOINCGUIRPCPort is the stock BOINC client's GUI RPC port; the Iris client
// never binds it.
const BOINCGUIRPCPort = 31416

func UserAgent() string {
	return Name + "/" + Version
}
