// Package poller queries Source servers over A2S and tracks their online state (B1-B4).
package poller

import (
	"context"
	"fmt"
	"time"

	"github.com/rumblefrog/go-a2s"
)

const (
	Timeout = 2 * time.Second
	Retries = 2
)

// Client is the subset of *a2s.Client the poller uses.
type Client interface {
	QueryInfo() (*a2s.ServerInfo, error)
	QueryPlayer() (*a2s.PlayerInfo, error)
	Close() error
}

// Dialer opens a client for one query cycle.
type Dialer func(addr string) (Client, error)

// DialA2S is the production Dialer. go-a2s has no context or retries, so Query adds them.
func DialA2S(addr string) (Client, error) {
	c, err := a2s.NewClient(addr, a2s.TimeoutOption(Timeout))
	if err != nil {
		return nil, err
	}
	return c, nil
}

// State is one successful poll. Players excludes bots.
type State struct {
	Name        string
	Map         string
	Players     int
	MaxPlayers  int
	PlayerNames []string
	// NamesKnown is false when players were present but the A2S_PLAYER query failed.
	NamesKnown bool
}

// Query runs A2S_INFO, and A2S_PLAYER only when at least one non-bot player is present.
// Each query is tried 1+Retries times. A failed A2S_PLAYER does not fail the poll.
func Query(ctx context.Context, c Client) (State, error) {
	info, err := retry(ctx, c.QueryInfo)
	if err != nil {
		return State{}, err
	}
	humans := int(info.Players) - int(info.Bots)
	if humans < 0 {
		humans = 0
	}
	st := State{Name: info.Name, Map: info.Map, Players: humans, MaxPlayers: int(info.MaxPlayers), NamesKnown: true}
	if humans == 0 {
		return st, nil
	}
	pi, err := retry(ctx, c.QueryPlayer)
	if err != nil {
		st.NamesKnown = false
		return st, nil
	}
	for _, p := range pi.Players {
		st.PlayerNames = append(st.PlayerNames, p.Name)
	}
	return st, nil
}

func retry[T any](ctx context.Context, f func() (T, error)) (v T, err error) {
	for i := 0; i <= Retries; i++ {
		if err = ctx.Err(); err != nil {
			return v, err
		}
		if v, err = safe(f); err == nil {
			return v, nil
		}
	}
	return v, err
}

// safe turns a panic from go-a2s on a truncated reply into an error.
func safe[T any](f func() (T, error)) (v T, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("malformed reply: %v", r)
		}
	}()
	return f()
}
