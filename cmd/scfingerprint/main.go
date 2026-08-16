// Command scfingerprint wraps the library for non-Go users and dataset
// curation workflows.
//
// Usage:
//
//	scfingerprint match <replay.rep>                    # who is each player? vs built-in dataset
//	scfingerprint match --player 2 --dir replays/       # multi-game evidence for one identity
//	scfingerprint match --name FlaSh --dir replays/     # same, selecting the player by name
//	scfingerprint same --a dirA/ --b dirB/              # are these two players the same human?
//	scfingerprint enroll --label "C9_FlaSh" --dir reps/ # build a fingerprint file
//	scfingerprint extract <replay.rep>                  # dump feature vectors (JSON) for debugging
//	scfingerprint dataset verify                        # hygiene checks over the built-in dataset
//
// Output is a human-readable table by default; pass --json for machines.
//
// Exit codes: 0 = match found / success, 1 = no match / findings, 2 = error.
package main

import (
	"fmt"
	"os"
)

const (
	exitOK      = 0
	exitNoMatch = 1
	exitError   = 2
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return exitError
	}
	switch args[0] {
	case "match":
		return cmdMatch(args[1:])
	case "same":
		return cmdSame(args[1:])
	case "enroll":
		return cmdEnroll(args[1:])
	case "extract":
		return cmdExtract(args[1:])
	case "dataset":
		if len(args) >= 2 && args[1] == "verify" {
			return cmdDatasetVerify(args[2:])
		}
		fmt.Fprintln(os.Stderr, "error: unknown dataset subcommand (want: verify)")
		return exitError
	case "-h", "--help", "help":
		usage()
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "error: unknown command %q\n", args[0])
		usage()
		return exitError
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `scfingerprint — identify StarCraft: Brood War players by how they play

Usage:
  scfingerprint match <replay.rep> [--json] [--min-z 2.0] [--min-confidence high]
  scfingerprint match (--player N | --name NAME) --dir replays/ [--json]
  scfingerprint same --a <dir|.rep> --b <dir|.rep> [--name-a NAME] [--name-b NAME] [--json]
  scfingerprint enroll --label LABEL (--dir replays/ | <replay.rep>...) [--name NAME] [-o out.json]
  scfingerprint extract <replay.rep> [--json]
  scfingerprint dataset verify [--json]

Exit codes: 0 = match found / success, 1 = no match / findings, 2 = error.
`)
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "error:", err)
	return exitError
}
