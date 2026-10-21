package main

import (
	"fmt"
	"os"
	"syscall"
)

// safePath is the only PATH used once running as root.
const safePath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// keptEnv are the caller's variables still honoured as root: only the proxy
// settings, which the network checks may need.
var keptEnv = []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"}

// dropToRoot handles running as a setuid-root binary. The real uid becomes
// root too, so k3s and the other tools see a plain root process, and the
// caller's environment is replaced: an inherited PATH or KUBECONFIG would let
// any user make a root process run their own binaries or read arbitrary files.
func dropToRoot() error {
	if os.Geteuid() != 0 || os.Getuid() == 0 {
		return nil
	}
	if err := syscall.Setgid(0); err != nil {
		return fmt.Errorf("setgid: %w", err)
	}
	if err := syscall.Setuid(0); err != nil {
		return fmt.Errorf("setuid: %w", err)
	}
	kept := map[string]string{}
	for _, k := range keptEnv {
		if v, ok := os.LookupEnv(k); ok {
			kept[k] = v
		}
	}
	os.Clearenv()
	for k, v := range kept {
		os.Setenv(k, v)
	}
	os.Setenv("PATH", safePath)
	os.Setenv("HOME", "/root")
	os.Setenv("LANG", "C.UTF-8")
	return nil
}
