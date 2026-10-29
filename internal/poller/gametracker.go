package poller

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GameTracker is the fallback data source when A2S gets no answer (B5). It reads the
// public server page. It is HTML, not an API, so the parser is strict: anything it does
// not understand is an error, and an error means the server stays offline.

const (
	SourceA2S         = "a2s"
	SourceGameTracker = "gametracker"

	// MaxGameTrackerAge is how old the page's scan may be before the data is ignored.
	MaxGameTrackerAge = 20 * time.Minute
	// FallbackEvery is the least time between fetches for one server.
	FallbackEvery = 5 * time.Minute

	maxPageBytes = 2 << 20
	userAgent    = "sourcewatch/1.0 (+https://github.com/kritzerenkrieg/sourcewatch)"
)

// ErrNotUnderstood means the page was fetched but did not have the expected content.
var ErrNotUnderstood = errors.New("gametracker page not understood")

// GTResult is what one page says. When Alive is false nothing else is set.
type GTResult struct {
	Alive     bool
	State     State
	Age       time.Duration // how long before the fetch GameTracker last scanned the server
	ScannedAt time.Time
}

// Fallback fetches the fallback data for addr ("host:port").
type Fallback interface {
	Fetch(ctx context.Context, addr string, now time.Time) (GTResult, error)
}

var (
	reScanned = regexp.MustCompile(`(?s)id="last_scanned"[^>]*>\s*Last scanned\s+(.*?)\s+ago`)
	reAgePart = regexp.MustCompile(`(\d+)\s*(day|hour|minute|second)s?`)
	reName    = regexp.MustCompile(`(?s)Name:\s*</span>\s*<a[^>]*>\s*<b>(.*?)</b>`)
	reAddr    = regexp.MustCompile(`(?s)Address:\s*</span>\s*([0-9.]+)\s*(?:&nbsp;)?\s*<span[^>]*>\s*Port:\s*</span>\s*(\d+)`)
	reStatus  = regexp.MustCompile(`(?s)Status:\s*</span>\s*<span[^>]*>\s*([^<]*?)\s*</span>`)
	reNum     = regexp.MustCompile(`id="HTML_num_players">\s*(\d+)\s*<`)
	reMax     = regexp.MustCompile(`id="HTML_max_players">\s*(\d+)\s*<`)
	reBots    = regexp.MustCompile(`id="HTML_num_bots">\s*(\d+)\s*<`)
	reMap     = regexp.MustCompile(`(?s)id="HTML_curr_map">\s*(.*?)\s*</div>`)
	reTable   = regexp.MustCompile(`(?s)ONLINE PLAYERS.*?<table[^>]*>(.*?)</table>`)
	reRow     = regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	reCell    = regexp.MustCompile(`(?s)<td([^>]*)>(.*?)</td>`)
	reTag     = regexp.MustCompile(`(?s)<[^>]*>`)
)

// ParseGameTracker reads a server page. addr is the "ip:port" that was asked for; a page
// about another server is rejected. now is the time of the fetch.
func ParseGameTracker(page []byte, addr string, now time.Time) (GTResult, error) {
	s := string(page)
	notUnderstood := func(what string) (GTResult, error) {
		return GTResult{}, fmt.Errorf("%w: %s", ErrNotUnderstood, what)
	}

	m := reScanned.FindStringSubmatch(s)
	if m == nil {
		return notUnderstood("no scan time")
	}
	age, ok := parseAge(m[1])
	if !ok {
		return notUnderstood("scan time " + strconv.Quote(m[1]))
	}
	res := GTResult{Age: age, ScannedAt: now.Add(-age)}

	// The page must be about the server we asked for.
	am := reAddr.FindStringSubmatch(s)
	if am == nil {
		return notUnderstood("no address")
	}
	if got := am[1] + ":" + am[2]; got != addr {
		return notUnderstood("page is for " + got)
	}

	// No "Status: Alive" means offline, including when the field is missing (B5).
	if sm := reStatus.FindStringSubmatch(s); sm == nil || !strings.EqualFold(strings.TrimSpace(sm[1]), "Alive") {
		return res, nil
	}

	nm, mm, num, max := reName.FindStringSubmatch(s), reMap.FindStringSubmatch(s), reNum.FindStringSubmatch(s), reMax.FindStringSubmatch(s)
	if nm == nil || mm == nil || num == nil || max == nil {
		return notUnderstood("missing name, map or player count")
	}
	players, _ := strconv.Atoi(num[1])
	maxPlayers, _ := strconv.Atoi(max[1])
	if bm := reBots.FindStringSubmatch(s); bm != nil { // A2S counts exclude bots, so do these
		bots, _ := strconv.Atoi(bm[1])
		players -= bots
	}
	if players < 0 {
		players = 0
	}
	res.Alive = true
	res.State = State{
		Name:       text(nm[1]),
		Map:        text(mm[1]),
		Players:    players,
		MaxPlayers: maxPlayers,
	}

	if tm := reTable.FindStringSubmatch(s); tm != nil {
		for _, row := range reRow.FindAllStringSubmatch(tm[1], -1) {
			cells := reCell.FindAllStringSubmatch(row[1], -1)
			if len(cells) < 4 || strings.Contains(cells[1][1], "col_h") {
				continue // header or malformed row
			}
			if name := text(cells[1][2]); name != "" {
				res.State.PlayerNames = append(res.State.PlayerNames, name)
			}
		}
		res.State.NamesKnown = true
	} else {
		// No table. That is fine for an empty server, but then the names are unknown.
		res.State.NamesKnown = players == 0
	}
	return res, nil
}

// parseAge turns "6 minutes 59 seconds" into a duration.
func parseAge(s string) (time.Duration, bool) {
	parts := reAgePart.FindAllStringSubmatch(s, -1)
	if len(parts) == 0 {
		return 0, false
	}
	var d time.Duration
	for _, p := range parts {
		n, _ := strconv.Atoi(p[1])
		unit := map[string]time.Duration{"day": 24 * time.Hour, "hour": time.Hour, "minute": time.Minute, "second": time.Second}[p[2]]
		d += time.Duration(n) * unit
	}
	return d, true
}

// text strips tags and decodes entities from a bit of page content.
func text(s string) string {
	return strings.TrimSpace(html.UnescapeString(reTag.ReplaceAllString(s, "")))
}

// GameTracker fetches pages from gametracker.com.
type GameTracker struct {
	Client  *http.Client
	BaseURL string
	// LookupIP resolves a host name. The page is addressed by IP.
	LookupIP func(host string) ([]net.IP, error)
}

func NewGameTracker() *GameTracker {
	return &GameTracker{
		Client:   &http.Client{Timeout: 10 * time.Second},
		BaseURL:  "https://www.gametracker.com",
		LookupIP: net.LookupIP,
	}
}

// Fetch downloads and parses the page for addr, a "host:port" whose host may be a name.
func (g *GameTracker) Fetch(ctx context.Context, addr string, now time.Time) (GTResult, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return GTResult{}, err
	}
	ipAddr, err := g.ipv4(host)
	if err != nil {
		return GTResult{}, err
	}
	target := ipAddr + ":" + port
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.BaseURL+"/server_info/"+target+"/", nil)
	if err != nil {
		return GTResult{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := g.Client.Do(req)
	if err != nil {
		return GTResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return GTResult{}, fmt.Errorf("gametracker returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
	if err != nil {
		return GTResult{}, err
	}
	return ParseGameTracker(body, target, now)
}

func (g *GameTracker) ipv4(host string) (string, error) {
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		return ip.String(), nil
	}
	ips, err := g.LookupIP(host)
	if err != nil {
		return "", err
	}
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip.String(), nil
		}
	}
	return "", fmt.Errorf("no IPv4 address for %s", host)
}
