package telegram

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/transport"
)

type fakeResolver struct{ seen [][]tg.DCOption }

func (f *fakeResolver) Primary(_ context.Context, _ int, l dcs.List) (transport.Conn, error) {
	f.seen = append(f.seen, l.Options)
	return nil, errors.New("no network in test")
}
func (f *fakeResolver) MediaOnly(context.Context, int, dcs.List) (transport.Conn, error) {
	return nil, nil
}
func (f *fakeResolver) CDN(context.Context, int, dcs.List) (transport.Conn, error) { return nil, nil }

func TestMediaResolverUsesMediaServer(t *testing.T) {
	f := &fakeResolver{}
	r := mediaResolver{plain: f}
	list := dcs.List{Options: []tg.DCOption{{ID: 4, IPAddress: "149.154.167.91", Port: 443}}}
	_, _ = r.Primary(context.Background(), 4, list)
	if len(f.seen) != 2 || f.seen[0][0].IPAddress != "149.154.164.250" || f.seen[0][0].MediaOnly {
		t.Fatalf("first try must be the media server: %+v", f.seen)
	}
	if f.seen[1][0].IPAddress != "149.154.167.91" {
		t.Fatal("fallback must be the normal server")
	}
	f.seen = nil
	_, _ = r.Primary(context.Background(), 5, dcs.List{Options: []tg.DCOption{{ID: 5, IPAddress: "91.108.56.130"}}})
	if len(f.seen) != 1 {
		t.Fatal("DC5 has no media server: normal only")
	}
}
