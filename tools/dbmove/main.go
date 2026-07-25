package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

// validateFlags rejects combining --only and --from. --from starts the plan
// partway through by ID and --only restricts the plan to a single ID; if both
// are set and the ID a caller passed to --only never comes at or after --from
// in the ordered plan, the loop below skips every step and exits 0 with
// nothing run. Erroring out up front is simpler than defining a precedence
// rule between the two.
func validateFlags(only, from string) error {
	if only != "" && from != "" {
		return fmt.Errorf("--only and --from are mutually exclusive")
	}
	return nil
}

func main() {
	cfgPath := flag.String("config", "", "path to move config yaml")
	execute := flag.Bool("execute", false, "actually run (default is dry-run)")
	only := flag.String("only", "", "run only the step with this ID")
	from := flag.String("from", "", "start at the step with this ID")
	reclaim := flag.Bool("reclaim", false, "run only the gated source-reclaim step (destructive; also needs --execute --confirm-reclaim)")
	confirmReclaim := flag.Bool("confirm-reclaim", false, "explicit confirmation required by --reclaim before anything is deleted")
	flag.Parse()

	if err := validateFlags(*only, *from); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}
	var steps []Step
	if *reclaim {
		steps = []Step{&reclaimStep{cfg: cfg, confirmReclaim: *confirmReclaim}}
	} else {
		steps = BuildPlan(cfg)
	}
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
