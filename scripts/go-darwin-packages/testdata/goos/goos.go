package goos

import "runtime"

func Mac() bool { return runtime.GOOS == "darwin" }
