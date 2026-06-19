// Command bibid is the bibid HTTP daemon. It serves the bibicontrol web UI and
// REST API bound to loopback (default 127.0.0.1:8080).
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/user"

	"github.com/asemones/bibicontrol/api"
)

func main() {
	var root string
	var addr string
	var owner string

	flag.StringVar(&root, "root", "", "workspace root directory (required)")
	flag.StringVar(&addr, "addr", "127.0.0.1:8080", "listen address (loopback only)")
	flag.StringVar(&owner, "owner", defaultOwner(), "workspace owner namespace")
	flag.Parse()

	if root == "" {
		fmt.Fprintln(os.Stderr, "bibid: --root is required")
		os.Exit(1)
	}

	d := api.New(root, owner)
	defer func() { _ = d.Close() }()

	log.Printf("bibid: listening on http://%s", addr)
	if err := http.ListenAndServe(addr, d.Handler()); err != nil {
		log.Fatalf("bibid: %v", err)
	}
}

// defaultOwner returns the current user's username, falling back to the USER
// environment variable and then "owner" if both fail.
func defaultOwner() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if v := os.Getenv("USER"); v != "" {
		return v
	}
	return "owner"
}
