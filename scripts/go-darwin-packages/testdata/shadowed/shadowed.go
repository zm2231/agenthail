package shadowed

import "runtime"

type target struct{ GOOS string }

var runtimeTarget = target{GOOS: "darwin"}

func Mac() bool { return runtimeTarget.GOOS == "darwin" && runtime.NumCPU() > 0 }
