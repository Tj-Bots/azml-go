package telegram

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

// UserClientPool manages multiple parallel, layer-initialized telegram.Client
// connections for a single user session to achieve maximum throughput (40+ MB/s)
// without triggering CONNECTION_LAYER_INVALID or head-of-line blocking.
type UserClientPool struct {
	mu      sync.RWMutex
	primary *tg.Client
	clients []*tg.Client
	cancel  context.CancelFunc
}

// StartUserClientPool initializes up to poolSize concurrent telegram.Client instances.
// Each instance has its own dedicated TCP socket and runs a full MTProto connection lifecycle
// (including InvokeWithLayer + InitConnection), guaranteeing stability and preventing layer invalidation.
func StartUserClientPool(
	parentCtx context.Context,
	appID int,
	appHash string,
	sessionData *session.Data,
	primaryClient *tg.Client,
	poolSize int,
) (*UserClientPool, error) {
	if poolSize <= 1 || sessionData == nil {
		return &UserClientPool{
			primary: primaryClient,
			clients: []*tg.Client{primaryClient},
		}, nil
	}

	poolCtx, poolCancel := context.WithCancel(parentCtx)
	p := &UserClientPool{
		primary: primaryClient,
		clients: make([]*tg.Client, 0, poolSize),
		cancel:  poolCancel,
	}
	if primaryClient != nil {
		p.clients = append(p.clients, primaryClient)
	}

	var wg sync.WaitGroup
	var clientMu sync.Mutex

	for i := 1; i < poolSize; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			storage := &session.StorageMemory{}
			sLoader := session.Loader{Storage: storage}
			dataCopy := *sessionData
			if err := sLoader.Save(context.Background(), &dataCopy); err != nil {
				return
			}

			subClient := telegram.NewClient(appID, appHash, telegram.Options{
				SessionStorage: storage,
				NoUpdates:      true,
			})

			ready := make(chan *tg.Client, 1)

			go func() {
				_ = subClient.Run(poolCtx, func(cCtx context.Context) error {
					ready <- subClient.API()
					<-cCtx.Done()
					return cCtx.Err()
				})
			}()

			select {
			case api := <-ready:
				clientMu.Lock()
				p.clients = append(p.clients, api)
				clientMu.Unlock()
			case <-time.After(8 * time.Second):
			case <-poolCtx.Done():
				return
			}
		}(i)
	}

	wg.Wait()
	log.Printf("[AZML-Go User Pool] Active parallel MTProto upload sockets: %d/%d", len(p.clients), poolSize)
	return p, nil
}

// Clients returns a thread-safe slice of all active clients in the pool.
func (p *UserClientPool) Clients() []*tg.Client {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	res := make([]*tg.Client, len(p.clients))
	copy(res, p.clients)
	return res
}

// Primary returns the primary client.
func (p *UserClientPool) Primary() *tg.Client {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.primary
}

// Close gracefully terminates all sub-client connections.
func (p *UserClientPool) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	p.clients = nil
	p.primary = nil
}

var (
	activePoolMu      sync.RWMutex
	activePoolClients []*tg.Client
)

// SetActiveUserPool keeps the user session connections for downloads too.
func SetActiveUserPool(clients []*tg.Client) {
	activePoolMu.Lock()
	activePoolClients = clients
	activePoolMu.Unlock()
}

// ActiveUserPool returns the user session connections (nil when there is no pool).
func ActiveUserPool() []*tg.Client {
	activePoolMu.RLock()
	defer activePoolMu.RUnlock()
	return append([]*tg.Client(nil), activePoolClients...)
}

var (
	activeTDMu   sync.RWMutex
	activeUserTD *telegram.Client
	activeBotTD  *telegram.Client
)

// SetActiveTD keeps the running clients so downloads can open connections on the file's DC.
func SetActiveTD(bot, user *telegram.Client) {
	activeTDMu.Lock()
	if bot != nil {
		activeBotTD = bot
	}
	activeUserTD = user
	activeTDMu.Unlock()
}

// ActiveUserTD / ActiveBotTD return the running clients (nil when not running).
func ActiveUserTD() *telegram.Client {
	activeTDMu.RLock()
	defer activeTDMu.RUnlock()
	return activeUserTD
}

func ActiveBotTD() *telegram.Client {
	activeTDMu.RLock()
	defer activeTDMu.RUnlock()
	return activeBotTD
}
