// Command livetest is a throwaway manual client used to verify the headless DLL
// over the network. It dials the running game's IPC server and exercises
// INFO / STOP / RESUME / RELOAD. Not part of the shipped tooling.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/asemones/bibicontrol/ipc"
	"github.com/asemones/bibicontrol/simctl"
)

func withTO() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 8*time.Second)
}

func main() {
	addr := "127.0.0.1:43100"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}

	// Wait for the game's IPC server to come up (boot + scene load take a while).
	var sess *ipc.Session
	deadline := time.Now().Add(60 * time.Second)
	for {
		dctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		s, err := ipc.Dial(dctx, addr, nil)
		cancel()
		if err == nil {
			sess = s
			break
		}
		if time.Now().After(deadline) {
			fmt.Println("FAILED to connect to", addr, "-", err)
			os.Exit(1)
		}
		time.Sleep(1 * time.Second)
	}
	defer sess.Close()
	fmt.Println("CONNECTED to", addr)

	c := simctl.New(sess)

	dump := func(label string, v any, err error) {
		if err != nil {
			fmt.Printf("%-20s ERROR: %v\n", label, err)
			return
		}
		b, _ := json.Marshal(v)
		fmt.Printf("%-20s %s\n", label, string(b))
	}

	// The IPC server is up at game boot, but the world loads a bit later. Poll
	// INFO until the simulation is running (Info errors with "simulation not
	// running" until the Main scene is live).
	ready := false
	until := time.Now().Add(90 * time.Second)
	for time.Now().Before(until) {
		ctx, cancel := withTO()
		r, err := c.Info(ctx)
		cancel()
		if err == nil {
			dump("INFO (initial)", r, nil)
			ready = true
			break
		}
		fmt.Println("waiting for sim...", err)
		time.Sleep(2 * time.Second)
	}
	if !ready {
		fmt.Println("sim never became ready; the IPC server answered but no world loaded")
		return
	}

	ctx, cancel := withTO()
	rs, es := c.Stop(ctx)
	cancel()
	dump("STOP", rs, es)

	ctx, cancel = withTO()
	r2, e2 := c.Info(ctx)
	cancel()
	dump("INFO (after STOP)", r2, e2)

	ctx, cancel = withTO()
	rr, er := c.Resume(ctx, 5.0)
	cancel()
	dump("RESUME x5", rr, er)

	ctx, cancel = withTO()
	r3, e3 := c.Info(ctx)
	cancel()
	dump("INFO (after RESUME)", r3, e3)

	ctx, cancel = withTO()
	rl, el := c.Reload(ctx)
	cancel()
	dump("RELOAD", rl, el)

	time.Sleep(8 * time.Second) // allow the scene reload to complete

	ctx, cancel = withTO()
	r4, e4 := c.Info(ctx)
	cancel()
	dump("INFO (after RELOAD)", r4, e4)

	fmt.Println("DONE")
}
