package bot

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/kritzerenkrieg/sourcewatch/internal/store"
)

// Limits on how much one place can ask the bot to poll (not set by the user, see E11).
const (
	maxServersPerScope      = 3
	maxNotificationsPerUser = 25
)

// runServer handles /server add and /server remove.
func runServer(b *Bot, in *discordgo.Interaction, list []*discordgo.ApplicationCommandInteractionDataOption) string {
	a := who(in)
	if a.InGuild && !a.CanManage {
		return "You need the Manage Server permission to change which game servers this Discord server watches."
	}
	if len(list) == 0 {
		return "Use `/server add` to watch a game server, or `/server remove` to stop."
	}
	address := toOpts(list[0].Options).str("address")
	switch list[0].Name {
	case "add":
		return b.serverAdd(a, address)
	case "remove":
		return b.serverRemove(a, address)
	}
	return "Use `/server add` to watch a game server, or `/server remove` to stop."
}

func (b *Bot) serverAdd(a actor, raw string) string {
	addr, err := normalizeAddr(raw)
	if err != nil {
		return err.Error()
	}
	cur, err := b.deps.Store.ListenerServers(a.Scope)
	if err != nil {
		return b.fail("list servers", err)
	}
	for _, r := range cur {
		if r.Addr == addr {
			return fmt.Sprintf("Already watching `%s`.", addr)
		}
	}
	if len(cur) >= maxServersPerScope {
		return fmt.Sprintf("You can watch at most %d game servers here. Remove one first.", maxServersPerScope)
	}
	if _, added, err := b.deps.Store.AddListener(a.Scope, addr, a.UserID, time.Now()); err != nil {
		return b.fail("add server", err)
	} else if added {
		b.deps.Watcher.Watch(addr)
	}
	return fmt.Sprintf("✅ Now watching `%s`.\nNext: `/notify` to get a notification, or `/status` in about a minute to see the server.", addr)
}

func (b *Bot) serverRemove(a actor, raw string) string {
	cur, err := b.deps.Store.ListenerServers(a.Scope)
	if err != nil {
		return b.fail("list servers", err)
	}
	ref, ok := matchServer(cur, raw)
	if !ok {
		return fmt.Sprintf("`%s` is not one of the game servers you watch. Pick one from the list.", clip(raw))
	}
	removed, err := b.deps.Store.RemoveListener(a.Scope, ref.Addr)
	if err != nil {
		return b.fail("remove server", err)
	}
	if removed {
		b.deps.Watcher.Unwatch(ref.Addr)
	}
	return fmt.Sprintf("✅ Stopped watching `%s`. Notifications created for it here were removed.", ref.Addr)
}

// matchServer finds the server a user typed or picked, with or without the default port.
func matchServer(list []store.ServerRef, raw string) (store.ServerRef, bool) {
	raw = strings.TrimSpace(raw)
	for _, r := range list {
		if strings.EqualFold(r.Addr, raw) || strings.EqualFold(r.Addr, raw+":"+defaultPort) {
			return r, true
		}
	}
	return store.ServerRef{}, false
}

func (b *Bot) fail(what string, err error) string {
	b.log.Error(what, "err", err)
	return "Something went wrong on my side. Please try again."
}
