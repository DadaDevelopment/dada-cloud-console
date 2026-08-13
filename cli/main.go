// Command ddc is the Dada Cloud one-button CLI. v0 supports exactly two
// things: `ddc login` (OAuth device authorization grant) and `ddc deploy`
// (package the current directory, upload it, stream the build, print the
// live URL).
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dada-tuda/console/cli/internal/cliapp"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: ddc <command>")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  login            sign in via your browser (device code flow)")
	fmt.Fprintln(os.Stderr, "  deploy [dir]     package and deploy dir (default: current directory)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "flags for deploy:")
	fmt.Fprintln(os.Stderr, "  --name <name>    app name (default: derived from the directory name)")
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := cliapp.LoadConfig()

	switch os.Args[1] {
	case "login":
		if err := cliapp.Login(ctx, cfg, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "deploy":
		opts, err := parseDeployArgs(os.Args[2:])
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		if err := cliapp.Deploy(ctx, cfg, opts, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func parseDeployArgs(args []string) (cliapp.DeployOptions, error) {
	opts := cliapp.DeployOptions{Dir: "."}
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--name":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--name requires a value")
			}
			opts.AppName = args[i+1]
			i += 2
		default:
			if len(args[i]) > 0 && args[i][0] == '-' {
				return opts, fmt.Errorf("unknown flag %q", args[i])
			}
			opts.Dir = args[i]
			i++
		}
	}
	return opts, nil
}
