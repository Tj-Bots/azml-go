package telegram

import (
	"context"
	"log"
	"sync"
	"time"

	td "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/transport"
)

// Telegram's media servers (what Pyrogram/wzgram use for file downloads with is_media=True).
var mediaDCAddrs = map[int]string{
	2: "149.154.167.151",
	4: "149.154.164.250",
}

// mediaResolver sends every connection of this client to the media server of the DC
// (when there is one), with the account's normal auth key – like wzgram's media sessions.
type mediaResolver struct{ plain dcs.Resolver }

func (r mediaResolver) Primary(ctx context.Context, dc int, list dcs.List) (transport.Conn, error) {
	if ip, ok := mediaDCAddrs[dc]; ok {
		media := list
		media.Options = []tg.DCOption{{ID: dc, IPAddress: ip, Port: 443}}
		for _, o := range list.Options {
			if o.ID == dc && o.MediaOnly && !o.CDN && !o.Ipv6 && !o.TCPObfuscatedOnly {
				o.MediaOnly = false
				media.Options = append(media.Options, o)
			}
		}
		if conn, err := r.plain.Primary(ctx, dc, media); err == nil {
			return conn, nil
		} else {
			log.Printf("[Media Client] media server of DC%d not reachable (%v), using the normal one", dc, err)
		}
	}
	return r.plain.Primary(ctx, dc, list)
}

func (r mediaResolver) MediaOnly(ctx context.Context, dc int, list dcs.List) (transport.Conn, error) {
	return r.Primary(ctx, dc, list)
}

func (r mediaResolver) CDN(ctx context.Context, dc int, list dcs.List) (transport.Conn, error) {
	return r.plain.CDN(ctx, dc, list)
}

var (
	mediaMu     sync.Mutex
	mediaClient *td.Client
	mediaCancel context.CancelFunc
)

// StartMediaClient opens a second connection of the user session, only for downloads,
// on Telegram's media servers. A previous one is stopped.
func StartMediaClient(appID int, appHash, sessionStr string) {
	StopMediaClient()
	storage, err := ParseSessionString(context.Background(), sessionStr)
	if err != nil {
		log.Printf("[Media Client] bad session: %v", err)
		return
	}
	client := td.NewClient(appID, appHash, td.Options{
		SessionStorage: storage,
		Resolver:       mediaResolver{plain: dcs.Plain(dcs.PlainOptions{})},
		NoUpdates:      true,
	})
	ctx, cancel := context.WithCancel(context.Background())
	mediaMu.Lock()
	mediaCancel = cancel
	mediaMu.Unlock()
	ready := make(chan struct{})
	go func() {
		err := client.Run(ctx, func(ctx context.Context) error {
			if _, err := client.Self(ctx); err != nil {
				return err
			}
			mediaMu.Lock()
			mediaClient = client
			mediaMu.Unlock()
			log.Printf("[Media Client] user session connected to the media servers (DC%d)", client.Config().ThisDC)
			close(ready)
			<-ctx.Done()
			return nil
		})
		mediaMu.Lock()
		if mediaClient == client {
			mediaClient = nil
		}
		mediaMu.Unlock()
		if err != nil && ctx.Err() == nil {
			log.Printf("[Media Client] stopped: %v", err)
		}
	}()
	select {
	case <-ready:
	case <-time.After(20 * time.Second):
		log.Printf("[Media Client] not ready after 20s, downloads use the normal connection")
	}
}

// StopMediaClient closes the media connection.
func StopMediaClient() {
	mediaMu.Lock()
	defer mediaMu.Unlock()
	if mediaCancel != nil {
		mediaCancel()
		mediaCancel = nil
	}
	mediaClient = nil
}

// ActiveMediaTD is the media client of the user session (nil when not running).
func ActiveMediaTD() *td.Client {
	mediaMu.Lock()
	defer mediaMu.Unlock()
	return mediaClient
}
