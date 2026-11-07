// coder-browser-host is launched by Chrome, not as a standalone daemon.
package main

import (
	"fmt"
	"github.com/tjcoder-labs/cli/internal/nativehost"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "register" {
		p, e := os.Executable()
		if e == nil {
			var paths []string
			paths, e = nativehost.Register(p)
			for _, s := range paths {
				fmt.Println(s)
			}
		}
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: coder-browser-host register (otherwise launched by Chrome)")
		os.Exit(1)
	}
	if err := nativehost.Serve(os.Args[1], os.Stdin, os.Stdout, ""); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
