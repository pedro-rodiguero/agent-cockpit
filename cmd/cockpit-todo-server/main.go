// Command cockpit-todo-server keeps a copy of cockpit's to-do list on a server, serves the phone app and
// syncs the computers. docs/todo-server.md describes it.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/pedro-rodiguero/agent-cockpit/internal/auth"
	"github.com/pedro-rodiguero/agent-cockpit/internal/todoserver"
)

const usage = `cockpit-todo-server: the to-do list on a server of your own

  cockpit-todo-server serve          serve the app and the API (TODO_LISTEN, default 127.0.0.1:8740)
  cockpit-todo-server set-password   create the account, or reset its password: ends every session
  cockpit-todo-server reset-2fa      turn two-step verification off, for a lost phone

Environment: TODO_DATA (/var/lib/cockpit-todo), TODO_ORIGIN (required, https://…), TODO_ENC_KEY (for
2FA: openssl rand -hex 32), TODO_TRUST_PROXY (none or caddy), TODO_DEFAULT_GROUP (Tasks).
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve()
	case "set-password":
		err = setPassword()
	case "reset-2fa":
		err = withServer(func(s *todoserver.Server) error { return s.ResetMFA() })
		if err == nil {
			fmt.Println("two-step verification is off and every session has ended")
		}
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cockpit-todo-server:", err)
		os.Exit(1)
	}
}

func config() (todoserver.Config, error) {
	key, err := auth.ParseKey(os.Getenv("TODO_ENC_KEY"))
	if err != nil {
		return todoserver.Config{}, fmt.Errorf("TODO_ENC_KEY: %w", err)
	}
	trust := auth.ProxyNone
	switch v := os.Getenv("TODO_TRUST_PROXY"); v {
	case "", "none":
	case "caddy":
		trust = auth.ProxyCaddy
	default:
		return todoserver.Config{}, fmt.Errorf("TODO_TRUST_PROXY must be none or caddy, got %q", v)
	}
	return todoserver.Config{
		Data:         envOr("TODO_DATA", "/var/lib/cockpit-todo"),
		Origin:       os.Getenv("TODO_ORIGIN"),
		EncKey:       key,
		Trust:        trust,
		DefaultGroup: os.Getenv("TODO_DEFAULT_GROUP"),
		Static:       todoserver.Web(),
	}, nil
}

// withServer opens the data for a change from the command line. The running server keeps the account in
// memory and would overwrite the change, so it must be stopped first.
func withServer(fn func(*todoserver.Server) error) error {
	cfg, err := config()
	if err != nil {
		return err
	}
	if conn, err := net.DialTimeout("tcp", envOr("TODO_LISTEN", "127.0.0.1:8740"), 300*time.Millisecond); err == nil {
		conn.Close()
		return errors.New("the server is running and would undo this change: stop it first (systemctl stop cockpit-todo)")
	}
	s, err := todoserver.New(cfg)
	if err != nil {
		return err
	}
	return fn(s)
}

func serve() error {
	cfg, err := config()
	if err != nil {
		return err
	}
	if cfg.EncKey == nil {
		log.Print("TODO_ENC_KEY is not set: two-step verification is unavailable")
	}
	s, err := todoserver.New(cfg)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              envOr("TODO_LISTEN", "127.0.0.1:8740"),
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	log.Printf("serving %s on %s", cfg.Origin, srv.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// setPassword asks for the new password twice, without echo when on a terminal.
func setPassword() error {
	pw, err := ask("New password: ")
	if err != nil {
		return err
	}
	again, err := ask("Again: ")
	if err != nil {
		return err
	}
	if pw != again {
		return errors.New("the two passwords differ")
	}
	err = withServer(func(s *todoserver.Server) error { return s.SetPassword(pw) })
	if err == nil {
		fmt.Println("password set; every session has ended")
	}
	return err
}

var stdin = bufio.NewReader(os.Stdin)

func ask(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
