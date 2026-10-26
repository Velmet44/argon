package xcontext

import "os/exec"

func lookpath(n string) (string, error) { return exec.LookPath(n) }
