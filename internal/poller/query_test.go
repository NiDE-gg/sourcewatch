package poller

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rumblefrog/go-a2s"
)

var header = []byte{0xFF, 0xFF, 0xFF, 0xFF}

func cstr(b *bytes.Buffer, s string) { b.WriteString(s); b.WriteByte(0) }

func infoReply(players, bots byte) []byte {
	var b bytes.Buffer
	b.Write(header)
	b.WriteByte(0x49)
	b.WriteByte(17)
	cstr(&b, "Test Server")
	cstr(&b, "de_dust2")
	cstr(&b, "cstrike")
	cstr(&b, "Counter-Strike")
	b.Write([]byte{0x0A, 0x00}) // app id 10
	b.Write([]byte{players, 24, bots, 'd', 'l', 0, 1})
	cstr(&b, "1.0")
	return b.Bytes()
}

func playerReply(names ...string) []byte {
	var b bytes.Buffer
	b.Write(header)
	b.WriteByte(0x44)
	b.WriteByte(byte(len(names)))
	for i, n := range names {
		b.WriteByte(byte(i))
		cstr(&b, n)
		binary.Write(&b, binary.LittleEndian, int32(5))
		binary.Write(&b, binary.LittleEndian, math.Float32bits(60))
	}
	return b.Bytes()
}

var challenge = append(append([]byte{}, header...), 0x41, 1, 2, 3, 4)

// fakeServer answers A2S requests over UDP. A request without the challenge gets
// a challenge reply, and a request carrying it gets the real reply. info and
// players are the real replies; queries counts packets received.
func fakeServer(t *testing.T, info, players []byte) (addr string, queries *atomic.Int32) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	queries = new(atomic.Int32)
	go func() {
		buf := make([]byte, 1400)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			queries.Add(1)
			req := buf[:n]
			var reply []byte
			switch req[4] {
			case 0x54: // A2S_INFO: "Source Engine Query\0" then the challenge
				if n == 5+len("Source Engine Query")+1 {
					reply = challenge
				} else {
					reply = info
				}
			case 0x55: // A2S_PLAYER
				if bytes.Equal(req[5:], []byte{0xFF, 0xFF, 0xFF, 0xFF}) {
					reply = challenge
				} else {
					reply = players
				}
			}
			pc.WriteTo(reply, from)
		}
	}()
	return pc.LocalAddr().String(), queries
}

func dial(t *testing.T, addr string) Client {
	t.Helper()
	c, err := a2s.NewClient(addr, a2s.TimeoutOption(150*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestQueryHandshakeAndValidReply(t *testing.T) {
	addr, _ := fakeServer(t, infoReply(3, 1), playerReply("alice", "bob", "bot"))
	st, err := Query(context.Background(), dial(t, addr))
	if err != nil {
		t.Fatal(err)
	}
	if st.Name != "Test Server" || st.Map != "de_dust2" || st.Players != 2 || st.MaxPlayers != 24 {
		t.Fatalf("bad state %+v", st)
	}
	if len(st.PlayerNames) != 3 || !st.NamesKnown {
		t.Fatalf("bad names %+v", st)
	}
}

func TestQuerySkipsPlayersWhenOnlyBots(t *testing.T) {
	addr, queries := fakeServer(t, infoReply(2, 2), playerReply("bot1", "bot2"))
	st, err := Query(context.Background(), dial(t, addr))
	if err != nil || st.Players != 0 {
		t.Fatalf("got %+v, %v", st, err)
	}
	if n := queries.Load(); n != 2 { // info request + info request with challenge
		t.Fatalf("expected 2 packets (no A2S_PLAYER), got %d", n)
	}
}

func TestQueryMalformedReply(t *testing.T) {
	addr, queries := fakeServer(t, append(header, 0x49, 17, 'x'), nil) // truncated info
	_, err := Query(context.Background(), dial(t, addr))
	if err == nil {
		t.Fatal("want error for malformed reply")
	}
	if n := queries.Load(); n != 2*(1+Retries) {
		t.Fatalf("expected %d packets over %d attempts, got %d", 2*(1+Retries), 1+Retries, n)
	}
}

// fake is a scripted Client for retry and poller tests.
type fake struct {
	infoErrs int // number of QueryInfo calls that fail first
	calls    atomic.Int32
	players  atomic.Int32
	info     a2s.ServerInfo
}

func (f *fake) QueryInfo() (*a2s.ServerInfo, error) {
	if int(f.calls.Add(1)) <= f.infoErrs {
		return nil, context.DeadlineExceeded
	}
	return &f.info, nil
}
func (f *fake) QueryPlayer() (*a2s.PlayerInfo, error) {
	f.players.Add(1)
	return &a2s.PlayerInfo{}, nil
}
func (f *fake) Close() error { return nil }

func TestQueryRetries(t *testing.T) {
	f := &fake{infoErrs: Retries}
	if _, err := Query(context.Background(), f); err != nil {
		t.Fatalf("want success on last retry: %v", err)
	}
	f = &fake{infoErrs: Retries + 1}
	if _, err := Query(context.Background(), f); err == nil {
		t.Fatal("want error after retries are exhausted")
	}
	if n := f.calls.Load(); n != 1+Retries {
		t.Fatalf("want %d attempts, got %d", 1+Retries, n)
	}
}

func TestQueryTimeout(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0") // never answers
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	start := time.Now()
	_, err = Query(context.Background(), dial(t, pc.LocalAddr().String()))
	if err == nil {
		t.Fatal("want timeout error")
	}
	if d := time.Since(start); d < 3*150*time.Millisecond {
		t.Fatalf("returned after %v, want at least 3 timeouts", d)
	}
}

func TestQueryContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fake{}
	if _, err := Query(ctx, f); err == nil || f.calls.Load() != 0 {
		t.Fatalf("want immediate cancel, err=%v calls=%d", err, f.calls.Load())
	}
}
