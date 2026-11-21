//go:build !linux && !darwin

package claudesurface

func processEnv(int, string) string { return "" }
