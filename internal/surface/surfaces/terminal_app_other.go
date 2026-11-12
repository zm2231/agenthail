//go:build !linux && !darwin

package surfaces

func processEnv(int, string) string { return "" }
