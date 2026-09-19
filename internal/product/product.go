package product

const Name = "Iris"

const Version = "1.0.0"

const RepoURL = "https://github.com/alplix/iris"

const DefaultGUIRPCPort = 31418

func UserAgent() string {
	return Name + "/" + Version
}
