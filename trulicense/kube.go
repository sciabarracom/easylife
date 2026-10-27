package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Kube runs `k3s kubectl`; trulicense always runs as root.
type Kube struct{}

// kubectl runs a kubectl command, feeding it stdin when not empty.
func (k *Kube) kubectl(stdin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "k3s", append([]string{"kubectl"}, args...)...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), errors.New(msg)
	}
	return stdout.String(), nil
}

// checkCluster verifies that k3s answers and the openserverless namespace exists.
func (k *Kube) checkCluster() error {
	out, err := k.kubectl("", "get", "ns", namespace, "--ignore-not-found", "-o", "name")
	if err != nil {
		return fmt.Errorf("cannot access k3s: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		return fmt.Errorf("namespace %s not found: is OpenServerless installed?", namespace)
	}
	return nil
}
