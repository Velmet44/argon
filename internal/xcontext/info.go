// Package xcontext collects OS/shell/cwd/ls for each turn.
package xcontext

import (
	"os"
	"runtime"
	"sort"
)

type Info struct {
	OS    string
	Shell string
	CWD   string
	LsTop []string
}

func Collect() Info {
	return Info{OS: runtime.GOOS + "/" + runtime.GOARCH, Shell: DetectShell(), CWD: cwd(), LsTop: lsTop(30)}
}

func cwd() string {
	d, err := os.Getwd()
	if err != nil {
		return "."
	}
	return d
}

func lsTop(n int) []string {
	ents, err := os.ReadDir(".")
	if err != nil {
		return nil
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
	out := make([]string, 0, n)
	for _, e := range ents {
		s := e.Name()
		if e.IsDir() {
			s += "/"
		}
		out = append(out, s)
		if len(out) >= n {
			break
		}
	}
	return out
}
