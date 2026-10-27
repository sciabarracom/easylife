package main

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

const tokenSecret = "openserverless-user-provisioning-token"

// generateToken returns 32 random decimal digits.
func generateToken() (string, error) {
	var b strings.Builder
	ten := big.NewInt(10)
	for i := 0; i < 32; i++ {
		d, err := rand.Int(rand.Reader, ten)
		if err != nil {
			return "", err
		}
		b.WriteString(d.String())
	}
	return b.String(), nil
}

// provisionToken stores a new user provisioning token in its secret, asking
// before replacing an existing one. The token goes to kubectl on stdin, never
// on the command line or on disk.
func provisionToken(k *Kube, in *bufio.Reader) error {
	out, err := k.kubectl("", "-n", namespace, "get", "secret", tokenSecret, "--ignore-not-found", "-o", "name")
	if err != nil {
		return err
	}
	exists := strings.TrimSpace(out) != ""
	if exists && !confirm(in, "A user provisioning token already exists. Replace it?") {
		fmt.Println("User provisioning token kept.")
		return nil
	}

	token, err := generateToken()
	if err != nil {
		return err
	}
	if exists {
		if _, err := k.kubectl("", "-n", namespace, "delete", "secret", tokenSecret); err != nil {
			return err
		}
	}
	if _, err := k.kubectl(token, "-n", namespace, "create", "secret", "generic", tokenSecret,
		"--from-file=token=/dev/stdin"); err != nil {
		return fmt.Errorf("creating the user provisioning token: %v", err)
	}

	fmt.Printf("\nUser provisioning token: %s\n\nWrite down this token: it won't be shown again.\n", token)
	return nil
}
