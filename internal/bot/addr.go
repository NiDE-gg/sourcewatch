package bot

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

const defaultPort = "27015"

// lookupIP resolves a host. It is a variable so tests do not need DNS.
var lookupIP = net.LookupIP

// normalizeAddr turns user input into "host:port". Anyone with Manage Server can name
// an address the bot will send UDP packets to, so addresses that resolve to private,
// loopback or link-local ranges are refused. That keeps the bot from probing the
// network it runs in.
func normalizeAddr(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("give an address like `1.2.3.4:27015`")
	}
	if !strings.Contains(raw, ":") {
		raw += ":" + defaultPort
	}
	host, portStr, err := net.SplitHostPort(raw)
	if err != nil || host == "" {
		return "", fmt.Errorf("`%s` is not a valid address. Use `host:port`", clip(raw))
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("`%s` is not a valid port", clip(portStr))
	}
	if len(host) > 253 || !validHost(host) {
		return "", fmt.Errorf("`%s` is not a valid host name or IPv4 address", clip(host))
	}
	ips, err := lookupIP(host)
	if err != nil || len(ips) == 0 {
		return "", fmt.Errorf("I could not find `%s`", clip(host))
	}
	for _, ip := range ips {
		if ip.To4() == nil {
			continue // only IPv4 is polled
		}
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			return "", fmt.Errorf("`%s` is not a public address", clip(host))
		}
	}
	return strings.ToLower(host) + ":" + portStr, nil
}

// validHost allows letters, digits, dots and hyphens only.
func validHost(h string) bool {
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
		default:
			return false
		}
	}
	return true
}

// clip shortens user input before it is shown back.
func clip(s string) string {
	if r := []rune(s); len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return s
}
