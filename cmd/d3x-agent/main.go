// Commande d3x-agent : la partie de D3X déposée sur une seedbox. Elle lit et
// hache les fichiers sur place, sans interface ni serveur.
package main

import (
	"fmt"
	"os"

	"github.com/V3x0rV3x/D3X/internal/agent"
)

var version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "agent" {
		args = args[1:]
	}
	if err := agent.Run(version, args, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "erreur : "+err.Error())
		os.Exit(1)
	}
}
