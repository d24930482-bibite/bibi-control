// Command bibid is the bibid HTTP daemon. It serves the bibicontrol web UI and
// REST API bound to loopback (default 127.0.0.1:8080).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"syscall"
	"time"

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
	srv := &http.Server{Addr: addr, Handler: d.Handler()}

	// Trap SIGINT/SIGTERM so shutdown runs the daemon's Close — this closes every
	// cached workspace handle, which checkpoints each DuckDB so its write-ahead log
	// is truncated instead of growing across runs. Without this, killing the daemon
	// leaves the WAL dirty, and the next open replays it in memory and can fail
	// with "out of memory".
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("bibid: listening on http://%s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("bibid: %v", err)
		}
	}()

	<-ctx.Done()
	stop() // restore default handling so a second Ctrl+C force-quits if needed
	log.Print("bibid: shutting down, closing workspaces…")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("bibid: http shutdown: %v", err)
	}
	if err := d.Close(); err != nil {
		log.Printf("bibid: close workspaces: %v", err)
	}
	log.Print("bibid: stopped")
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
