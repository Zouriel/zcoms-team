// Command zcoms-team is the zc-team component: team coordination, task
// delegation, GitHub Projects sync, and automated standups. It owns no Telegram
// session — the core daemon does; this is a pure-Go process that persists to
// SQLite and serves commands on team.sock (driven by `zc team …` and, later, the
// bridge forwarding team commands).
package main

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"os"
	"path/filepath"

	"github.com/Zouriel/zcoms-sdk/agent"
	"github.com/Zouriel/zcoms-team/internal/db"
	"github.com/Zouriel/zcoms-team/internal/store"
)

func teamSocketPath() string {
	dir, err := agent.DefaultAppDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "team.sock")
	}
	return filepath.Join(dir, "team.sock")
}

type cmdRequest struct {
	Text  string `json:"text"`            // a `zc team …` command line
	Actor string `json:"actor,omitempty"` // who issued it (@username or local user)
}

type cmdResponse struct {
	OK    bool   `json:"ok"`
	Reply string `json:"reply,omitempty"`
	Error string `json:"error,omitempty"`
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
	serveCommands(s)
}

func serveCommands(s *store.Store) {
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
			actor := req.Actor
			if actor == "" {
				actor = "@owner"
			}
			reply, err := handleCommand(s, actor, req.Text)
			if err != nil {
				writeResp(c, cmdResponse{Error: err.Error()})
				return
			}
			writeResp(c, cmdResponse{OK: true, Reply: reply})
		}(conn)
	}
}

func writeResp(c net.Conn, r cmdResponse) {
	b, _ := json.Marshal(r)
	_, _ = c.Write(append(b, '\n'))
}
