package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func main() {
	cfgPath := flag.String("config", "", "path to move config yaml")
	execute := flag.Bool("execute", false, "actually run (default is dry-run)")
	only := flag.String("only", "", "run only the step with this ID")
	from := flag.String("from", "", "start at the step with this ID")
	flag.Parse()

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}
	steps := BuildPlan(cfg)
	dryRun := !*execute
	ctx := context.Background()
	var runner CommandRunner = execRunner{}
	started := *from == ""
	for _, s := range steps {
		if *only != "" && s.ID() != *only {
			continue
		}
		if !started {
			if s.ID() == *from {
				started = true
			} else {
				continue
			}
		}
		fmt.Printf("== %s: %s ==\n", s.ID(), s.Describe())
		if err := s.Run(ctx, runner, dryRun); err != nil {
			fmt.Fprintf(os.Stderr, "step %s failed: %v\n", s.ID(), err)
			os.Exit(1)
		}
	}
	if dryRun {
		fmt.Println("\n(dry-run: no mutations performed; pass --execute to run)")
	}
}
