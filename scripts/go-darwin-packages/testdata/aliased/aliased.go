package aliased

import goruntime "runtime"

func Mac() bool { return goruntime.GOOS == "darwin" }
