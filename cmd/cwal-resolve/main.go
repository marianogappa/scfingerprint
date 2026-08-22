// Command cwal-resolve lists the nickname → aurora_id mapping from the
// built-in CWAL.gg Player Tracker snapshot and optionally resolves each
// account to its current toons via the cwal.gg API.
//
// Usage:
//
//	cwal-resolve list                     # dump the nickname → aurora_id table
//	cwal-resolve resolve [--nick FlaSh]   # resolve all accounts to current toons
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/marianogappa/scfingerprint/cwal"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "list":
		cmdList()
	case "resolve":
		cmdResolve(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `cwal-resolve — CWAL.gg Player Tracker nickname → aurora_id mapping

Commands:
  list                              dump the nickname → aurora_id table (JSON)
  resolve [--nick X]                resolve aurora_ids to current toons via cwal.gg API
`)
}

func cmdList() {
	entries, err := cwal.LoadDefaultList()
	if err != nil {
		fatal(err)
	}
	data, _ := json.MarshalIndent(entries, "", " ")
	fmt.Println(string(data))
}

func cmdResolve(args []string) {
	fs := flag.NewFlagSet("resolve", flag.ExitOnError)
	nick := fs.String("nick", "", "resolve only this nickname")
	delay := fs.Duration("delay", 200*time.Millisecond, "delay between API requests")
	out := fs.String("o", "", "output file (default: stdout)")
	_ = fs.Parse(args)

	entries, err := cwal.LoadDefaultList()
	if err != nil {
		fatal(err)
	}

	if *nick != "" {
		e := cwal.LookupByNickname(entries, *nick)
		if e == nil {
			fatal(fmt.Errorf("nickname %q not found", *nick))
		}
		entries = []cwal.Entry{*e}
	}

	resolver := cwal.NewResolver()
	resolved, err := resolver.ResolveAll(entries, *delay)
	if err != nil {
		fatal(err)
	}

	data, _ := json.MarshalIndent(resolved, "", " ")
	if *out != "" {
		if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
			fatal(err)
		}
		fmt.Fprintf(os.Stderr, "wrote %s (%d entries)\n", *out, len(resolved))
	} else {
		fmt.Println(string(data))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(2)
}
