// trulicense activates a Trustant licence: it validates the licence signature,
// provisions in the openserverless namespace an nginx proxy that publishes the
// licensed hosts, and creates the user provisioning token. See
// ../spec/trulicense.md.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

const namespace = "openserverless"

func main() {
	reset := flag.Bool("reset", false, "remove the licence proxy and the user provisioning token")
	flag.Parse()

	if err := run(*reset); err != nil {
		fmt.Fprintf(os.Stderr, "trulicense: %v\n", err)
		os.Exit(1)
	}
}

func run(reset bool) error {
	if err := dropToRoot(); err != nil {
		return err
	}
	if os.Geteuid() != 0 {
		return errors.New("must run as root: use sudo trulicense")
	}
	k := &Kube{}
	if err := k.checkCluster(); err != nil {
		return err
	}
	in := bufio.NewReader(os.Stdin)

	if reset {
		return resetAll(k, in)
	}

	pub, err := masterKey()
	if err != nil {
		return err
	}
	fmt.Print("Paste the licence: ")
	token, err := in.ReadString('\n')
	if err != nil && strings.TrimSpace(token) == "" {
		return errors.New("no licence entered")
	}
	lic, err := parseLicense(token, pub, time.Now())
	if err != nil {
		return err
	}
	fmt.Printf("Licence for %s, issued %s, expires %s\n", lic.Sub, orNone(lic.Iat), orNone(lic.Exp))

	var hosts []string
	for _, h := range lic.Hosts {
		host, err := proxyHost(h)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: skipping %s: %v\n", h, err)
			continue
		}
		hosts = append(hosts, host)
	}
	if len(hosts) == 0 {
		return errors.New("the licence has no host to provision")
	}
	if err := provisionProxy(k, hosts); err != nil {
		return err
	}
	return provisionToken(k, in)
}

// confirm asks a yes/no question, defaulting to no.
func confirm(in *bufio.Reader, question string) bool {
	fmt.Printf("%s [y/N] ", question)
	answer, _ := in.ReadString('\n')
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

func orNone(s string) string {
	if s == "" {
		return "never"
	}
	return s
}

func resetAll(k *Kube, in *bufio.Reader) error {
	if !confirm(in, "Remove the licence proxy and the user provisioning token?") {
		fmt.Println("Nothing removed.")
		return nil
	}
	_, err := k.kubectl("", "-n", namespace, "delete", "--ignore-not-found",
		"deployment/"+proxyName, "service/"+proxyName, "ingress/"+proxyName,
		"configmap/"+proxyName, "secret/"+tokenSecret)
	if err != nil {
		return err
	}
	fmt.Println("Licence proxy and user provisioning token removed.")
	return nil
}
