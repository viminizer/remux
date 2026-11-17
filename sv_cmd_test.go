package main

import (
	"flag"
	"strings"
	"testing"
	"time"
)

func TestParseAllInterleaved(t *testing.T) {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	timeout := fs.Duration("timeout", time.Minute, "")
	if err := parseAll(fs, []string{"w03", "--timeout", "9m", "w04"}); err != nil {
		t.Fatal(err)
	}
	if *timeout != 9*time.Minute || strings.Join(fs.Args(), ",") != "w03,w04" {
		t.Fatalf("timeout %v args %v", *timeout, fs.Args())
	}
}
