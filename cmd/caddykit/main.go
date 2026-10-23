package main

import (
	"os"

	"caddykit/internal/cli"
)

var version = "dev"

func main() {
	os.Exit(cli.Run(os.Args[1:], version))
}
