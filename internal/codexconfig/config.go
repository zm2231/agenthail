package codexconfig

import "os"

const DefaultLaunchRemoteDebuggingPort = "9231"

func RemoteDebuggingPort() string {
	return os.Getenv("AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT")
}

func LaunchRemoteDebuggingPort() string {
	if port := RemoteDebuggingPort(); port != "" {
		return port
	}
	return DefaultLaunchRemoteDebuggingPort
}
