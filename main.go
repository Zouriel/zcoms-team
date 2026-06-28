// Command zcoms-team is the zc-team component: team coordination, task
// delegation, GitHub Projects sync, and automated standups. It owns no Telegram
// session — the core daemon does; this is a pure-Go process that persists to
// SQLite and serves commands on team.sock (driven by `zc team …` and, later, the
// bridge forwarding team commands).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	agentclient "github.com/Zouriel/zcoms-agent/client"
	"github.com/Zouriel/zcoms-agent/scheduler"
	"github.com/Zouriel/zcoms-team/internal/db"
	"github.com/Zouriel/zcoms-team/internal/store"
	commsclient "github.com/Zouriel/zcoms/client"
)

func teamSocketPath() string {
	dir, err := commsclient.DefaultAppDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "team.sock")
	}
	return filepath.Join(dir, "team.sock")
}

type cmdRequest struct {
	Text   string           `json:"text"`             // a `zc team …` command line
	Actor  string           `json:"actor,omitempty"`  // who issued it (@username or local user)
	Result *InterviewResult `json:"result,omitempty"` // a standup interview result posted back by errands
}

type cmdResponse struct {
	OK       bool   `json:"ok"`
	Reply    string `json:"reply,omitempty"`
	Continue bool   `json:"continue,omitempty"` // bridge keeps routing this actor here
	Error    string `json:"error,omitempty"`
}

func main() {
	log.SetFlags(log.LstdFlags)
	log.Println("[team] component starting")

	path, err := db.DefaultPath()
	if err != nil {
		log.Fatalf("[team] db path: %v", err)
	}
	d, err := db.Open(path)
	if err != nil {
		log.Fatalf("[team] open db: %v", err)
	}
	defer d.Close()
	log.Println("[team] db ready:", path)

	s := store.New(d)
	// The owner is configured in the agent tier (agent.db settings); resolve it
	// through agent/client. Tolerate the agent being down (mainUser stays empty).
	mainUser := ""
	if ac, err := agentclient.New(); err == nil {
		if v, err := ac.Command("settings get main_user", ""); err == nil {
			mainUser = v
		}
	}
	e := NewEngine(s, mainUser)

	// Comms client to the daemon (for posting standup reports to Telegram groups).
	// Tolerate the daemon being down — reports just won't post until it's up.
	var client *commsclient.Client
	if c, err := commsclient.NewDefault(); err == nil {
		client = c
	}
	co := NewCoordinator(e, client)

	// Standups register on the shared scheduler primitive instead of a hand-rolled
	// sleep loop. The team runs its own scheduler instance (it is a separate
	// process from the agent); the due-standup + periodic-report check is one
	// Interval job, so no `for { sleep }` loop lives outside the scheduler.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sched := scheduler.New()
	co.Register(sched)
	go sched.Run(ctx)

	serveCommands(e, co)
}

func serveCommands(e *Engine, co *Coordinator) {
	path := teamSocketPath()
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		log.Fatalf("[team] listen %s: %v", path, err)
	}
	_ = os.Chmod(path, 0o600)
	log.Println("[team] listening on", path)
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			defer func() {
				if r := recover(); r != nil {
					writeResp(c, cmdResponse{Error: "internal error"})
				}
			}()
			line, err := bufio.NewReader(c).ReadBytes('\n')
			if err != nil && len(line) == 0 {
				return
			}
			var req cmdRequest
			if json.Unmarshal(line, &req) != nil {
				writeResp(c, cmdResponse{Error: "bad request"})
				return
			}
			// A standup interview result posted back by the errands component.
			if req.Result != nil {
				co.OnResult(*req.Result)
				writeResp(c, cmdResponse{OK: true})
				return
			}
			actor := req.Actor
			if actor == "" {
				actor = "@owner"
			}
			reply, cont, err := e.Handle(actor, req.Text)
			if err != nil {
				writeResp(c, cmdResponse{Error: err.Error()})
				return
			}
			writeResp(c, cmdResponse{OK: true, Reply: reply, Continue: cont})
		}(conn)
	}
}

func writeResp(c net.Conn, r cmdResponse) {
	b, _ := json.Marshal(r)
	_, _ = c.Write(append(b, '\n'))
}
