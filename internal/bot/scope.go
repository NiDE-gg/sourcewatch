package bot

import (
	"strings"

	"github.com/bwmarrin/discordgo"
)

// actor is who ran a command and where. In a Discord server the scope is the server;
// in a DM with the bot it is the user.
type actor struct {
	UserID    string
	Scope     string // "guild:<id>" or "user:<id>"
	InGuild   bool
	CanManage bool // Manage Server or Administrator (E9)
	ChannelID string
}

func who(in *discordgo.Interaction) actor {
	if in.Member != nil && in.Member.User != nil {
		return actor{
			UserID:    in.Member.User.ID,
			Scope:     "guild:" + in.GuildID,
			InGuild:   true,
			CanManage: in.Member.Permissions&(discordgo.PermissionManageGuild|discordgo.PermissionAdministrator) != 0,
			ChannelID: in.ChannelID,
		}
	}
	id := ""
	if in.User != nil {
		id = in.User.ID
	}
	return actor{UserID: id, Scope: "user:" + id, ChannelID: in.ChannelID}
}

// opts gives typed access to the options of a subcommand.
type opts map[string]*discordgo.ApplicationCommandInteractionDataOption

func toOpts(list []*discordgo.ApplicationCommandInteractionDataOption) opts {
	o := opts{}
	for _, v := range list {
		o[v.Name] = v
	}
	return o
}

func (o opts) str(name string) string {
	if v, ok := o[name]; ok {
		if s, ok := v.Value.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func (o opts) int(name string) (int64, bool) {
	v, ok := o[name]
	if !ok {
		return 0, false
	}
	switch n := v.Value.(type) {
	case float64: // how JSON numbers arrive
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	}
	return 0, false
}
