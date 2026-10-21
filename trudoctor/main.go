// trudoctor checks a local OpenServerless/Trustant installation (DNS, API,
// k3s cluster, pods), gathers details on any anomaly it finds and prints a
// report with the relevant logs. See ../spec/trudoctor.md.
//
// Progress goes to stderr and the markdown report to stdout, so
// `trudoctor > report.md` saves just the report.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	namespace := flag.String("namespace", "openserverless", "namespace of the OpenServerless pods")
	tail := flag.Int("tail", 2000, "number of log lines collected per container")
	flag.Parse()

	if err := dropToRoot(); err != nil {
		fmt.Fprintf(os.Stderr, "trudoctor: %v\n", err)
		os.Exit(2)
	}

	d := newDoctor(*namespace, *tail)

	section("System")
	d.runSystemChecks()

	section("Kubernetes")
	d.runKubeChecks()

	section("Anomaly detection")
	d.runAnomalyDetection()

	section("Logs")
	d.collectLogs()

	fmt.Print(d.buildReport())

	if d.failed() > 0 {
		os.Exit(1)
	}
}

// progress prints a status line on stderr, keeping stdout for the report.
func progress(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func section(name string) {
	progress("\n=== %s ===", name)
}
