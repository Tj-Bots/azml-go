package telegram

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gotd/td/session"
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
type mediaResolver struct {
	plain dcs.Resolver
	on    *atomic.Bool // nil = always on
}

func (r mediaResolver) Primary(ctx context.Context, dc int, list dcs.List) (transport.Conn, error) {
	if ip, ok := mediaDCAddrs[dc]; ok && (r.on == nil || r.on.Load()) {
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

type mediaSlot struct {
	client *td.Client
	cancel context.CancelFunc
}

var (
	mediaMu    sync.Mutex
	mediaSlots = map[string]*mediaSlot{}
)

// StartMediaClient opens a second connection of the user session, only for downloads,
// on Telegram's media servers. A previous one is stopped.
func StartMediaClient(appID int, appHash, sessionStr string) {
	storage, err := ParseSessionString(context.Background(), sessionStr)
	if err != nil {
		log.Printf("[Media Client] bad session: %v", err)
		return
	}
	startMedia("user", appID, appHash, storage, "")
}

// StartBotMediaClient: the same for the bot (it logs in with the token once).
func StartBotMediaClient(appID int, appHash, botToken string) {
	startMedia("bot", appID, appHash, &session.StorageMemory{}, botToken)
}

func startMedia(kind string, appID int, appHash string, storage session.Storage, botToken string) {
	stopMedia(kind)
	on := &atomic.Bool{}
	client := td.NewClient(appID, appHash, td.Options{
		SessionStorage: storage,
		Resolver:       mediaResolver{plain: dcs.Plain(dcs.PlainOptions{}), on: on},
		NoUpdates:      true,
	})
	ctx, cancel := context.WithCancel(context.Background())
	mediaMu.Lock()
	mediaSlots[kind] = &mediaSlot{cancel: cancel}
	mediaMu.Unlock()
	ready := make(chan struct{})
	go func() {
		err := client.Run(ctx, func(ctx context.Context) error {
			if botToken != "" {
				if st, err := client.Auth().Status(ctx); err != nil || !st.Authorized {
					if _, err := client.Auth().Bot(ctx, botToken); err != nil {
						return err
					}
				}
			}
			if _, err := client.Self(ctx); err != nil {
				return err
			}
			on.Store(true) // from now on new connections go to the media servers
			mediaMu.Lock()
			if sl := mediaSlots[kind]; sl != nil {
				sl.client = client
			}
			mediaMu.Unlock()
			log.Printf("[Media Client] %s connected, downloads use the media servers (DC%d)", kind, client.Config().ThisDC)
			close(ready)
			<-ctx.Done()
			return nil
		})
		if err != nil && ctx.Err() == nil {
			log.Printf("[Media Client] %s stopped: %v", kind, err)
		}
		mediaMu.Lock()
		if sl := mediaSlots[kind]; sl != nil && sl.client == client {
			sl.client = nil
		}
		mediaMu.Unlock()
	}()
	select {
	case <-ready:
	case <-time.After(30 * time.Second):
		log.Printf("[Media Client] %s not ready after 30s, downloads use the normal connection", kind)
	}
}

func stopMedia(kind string) {
	mediaMu.Lock()
	defer mediaMu.Unlock()
	if sl := mediaSlots[kind]; sl != nil {
		if sl.cancel != nil {
			sl.cancel()
		}
		delete(mediaSlots, kind)
	}
}

// StopMediaClient closes the user session's media connection.
func StopMediaClient() { stopMedia("user") }

func activeMedia(kind string) *td.Client {
	mediaMu.Lock()
	defer mediaMu.Unlock()
	if sl := mediaSlots[kind]; sl != nil {
		return sl.client
	}
	return nil
}

// ActiveMediaTD is the media client of the user session (nil when not running).
func ActiveMediaTD() *td.Client { return activeMedia("user") }

// ActiveBotMediaTD is the media client of the bot (nil when not running).
func ActiveBotMediaTD() *td.Client { return activeMedia("bot") }
