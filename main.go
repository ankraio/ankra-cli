package main

import "ankra/cmd"

// version is overridden at build time via:
//
//	go build -ldflags "-X main.version=$(git describe --tags --always)"
var version = ""

func main() {
	cmd.SetVersion(version)
	cmd.Execute()
}
