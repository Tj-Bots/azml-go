package ext

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"azml-go/config"
	"azml-go/types"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type DatabaseManager struct {
	client            *mongo.Client
	usersCol          *mongo.Collection
	settingsCol       *mongo.Collection
	peersCol          *mongo.Collection
	isMongo           bool
	localFile         string
	localSettingsFile string
	fileLock          sync.Mutex
}

var (
	DB   *DatabaseManager
	once sync.Once
)

func GetDB() *DatabaseManager {
	return DB
}

func InitDB() *DatabaseManager {
	once.Do(func() {
		cfg := config.Get()
		mgr := &DatabaseManager{
			localFile:         "user_data.json",
			localSettingsFile: "bot_settings.json",
		}

		if cfg.DatabaseURL != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			clientOpts := options.Client().ApplyURI(cfg.DatabaseURL)
			client, err := mongo.Connect(ctx, clientOpts)
			if err == nil && client.Ping(ctx, nil) == nil {
				mgr.client = client
				name := databaseName(ctx, client, cfg.BotToken)
				mgr.usersCol = client.Database(name).Collection("users")
				mgr.settingsCol = client.Database(name).Collection("settings")
				mgr.peersCol = client.Database(name).Collection("peers")
				mgr.isMongo = true
				log.Printf("[DB] Connected to MongoDB successfully (database %s)", name)
			} else {
				log.Printf("[DB] MongoDB connection failed (%v), falling back to local file", err)
			}
		}

		if !mgr.isMongo {
			log.Println("[DB] Using local JSON storage for user & bot persistence")
		}

		DB = mgr
	})
	return DB
}

func (d *DatabaseManager) LoadAllUsers(userMap *types.SafeUserMap) error {
	if d.isMongo {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		cursor, err := d.usersCol.Find(ctx, bson.M{})
		if err != nil {
			return err
		}
		defer cursor.Close(ctx)

		for cursor.Next(ctx) {
			var u types.UserSettings
			if err := cursor.Decode(&u); err == nil {
				userMap.Set(&u)
			}
		}
		return cursor.Err()
	}

	d.fileLock.Lock()
	defer d.fileLock.Unlock()

	data, err := os.ReadFile(d.localFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var users map[int64]*types.UserSettings
	if err := json.Unmarshal(data, &users); err != nil {
		return err
	}

	for _, u := range users {
		userMap.Set(u)
	}
	return nil
}

func (d *DatabaseManager) SaveUser(u *types.UserSettings) error {
	if d.isMongo {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		opts := options.Replace().SetUpsert(true)
		_, err := d.usersCol.ReplaceOne(ctx, bson.M{"_id": u.UserID}, u, opts)
		return err
	}

	d.fileLock.Lock()
	defer d.fileLock.Unlock()

	var users map[int64]*types.UserSettings
	data, err := os.ReadFile(d.localFile)
	if err == nil {
		_ = json.Unmarshal(data, &users)
	}
	if users == nil {
		users = make(map[int64]*types.UserSettings)
	}

	users[u.UserID] = u
	newData, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(d.localFile, newData, 0644)
}

func (d *DatabaseManager) LoadAllBotSettings() (map[string]string, error) {
	if d == nil {
		return nil, nil
	}
	if d.isMongo {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		cursor, err := d.settingsCol.Find(ctx, bson.M{})
		if err != nil {
			return nil, err
		}
		defer cursor.Close(ctx)

		settings := make(map[string]string)
		for cursor.Next(ctx) {
			var doc struct {
				Key   string `bson:"_id"`
				Value string `bson:"value"`
			}
			if err := cursor.Decode(&doc); err == nil && doc.Key != "" {
				settings[doc.Key] = doc.Value
			}
		}
		return settings, cursor.Err()
	}

	d.fileLock.Lock()
	defer d.fileLock.Unlock()

	data, err := os.ReadFile(d.localSettingsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var settings map[string]string
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, err
	}
	return settings, nil
}

func (d *DatabaseManager) SaveBotSetting(key, val string) error {
	if d == nil {
		return nil
	}
	if d.isMongo {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		opts := options.Update().SetUpsert(true)
		_, err := d.settingsCol.UpdateOne(
			ctx,
			bson.M{"_id": key},
			bson.M{"$set": bson.M{"value": val, "updated_at": time.Now()}},
			opts,
		)
		return err
	}

	d.fileLock.Lock()
	defer d.fileLock.Unlock()

	settings := make(map[string]string)
	data, err := os.ReadFile(d.localSettingsFile)
	if err == nil {
		_ = json.Unmarshal(data, &settings)
	}

	if val == "" {
		delete(settings, key)
	} else {
		settings[key] = val
	}

	newData, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(d.localSettingsFile, newData, 0644)
}

func (d *DatabaseManager) GetBotSetting(key string) (string, error) {
	if d == nil {
		return "", nil
	}
	if d.isMongo {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var doc struct {
			Key   string `bson:"_id"`
			Value string `bson:"value"`
		}
		err := d.settingsCol.FindOne(ctx, bson.M{"_id": key}).Decode(&doc)
		if err != nil {
			return "", err
		}
		return doc.Value, nil
	}

	settings, err := d.LoadAllBotSettings()
	if err != nil {
		return "", err
	}
	return settings[key], nil
}

// ---- peers: Telegram access hashes, kept so channels/users resolve right after a restart ----

const localPeersFile = "peers.json"

// LoadPeers returns every saved access hash ("c:<id>" bot channels, "u:<id>" users,
// "uc:<id>" channels seen by the user session).
func (d *DatabaseManager) LoadPeers() map[string]int64 {
	out := map[string]int64{}
	if d == nil {
		return out
	}
	if d.isMongo && d.peersCol != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cur, err := d.peersCol.Find(ctx, bson.M{})
		if err != nil {
			log.Printf("[DB] Loading peers failed: %v", err)
			return out
		}
		defer cur.Close(ctx)
		for cur.Next(ctx) {
			var doc struct {
				Key  string `bson:"_id"`
				Hash int64  `bson:"hash"`
			}
			if cur.Decode(&doc) == nil && doc.Key != "" && doc.Hash != 0 {
				out[doc.Key] = doc.Hash
			}
		}
		return out
	}
	d.fileLock.Lock()
	defer d.fileLock.Unlock()
	if data, err := os.ReadFile(localPeersFile); err == nil {
		_ = json.Unmarshal(data, &out)
	}
	return out
}

// SavePeer stores one access hash (0 removes it).
func (d *DatabaseManager) SavePeer(key string, hash int64) {
	if d == nil || key == "" {
		return
	}
	if d.isMongo && d.peersCol != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var err error
		if hash == 0 {
			_, err = d.peersCol.DeleteOne(ctx, bson.M{"_id": key})
		} else {
			_, err = d.peersCol.UpdateOne(ctx, bson.M{"_id": key},
				bson.M{"$set": bson.M{"hash": hash, "updated_at": time.Now()}},
				options.Update().SetUpsert(true))
		}
		if err != nil {
			log.Printf("[DB] Saving peer %s failed: %v", key, err)
		}
		return
	}
	d.fileLock.Lock()
	defer d.fileLock.Unlock()
	peers := map[string]int64{}
	if data, err := os.ReadFile(localPeersFile); err == nil {
		_ = json.Unmarshal(data, &peers)
	}
	if hash == 0 {
		delete(peers, key)
	} else {
		peers[key] = hash
	}
	if data, err := json.Marshal(peers); err == nil {
		_ = os.WriteFile(localPeersFile, data, 0644)
	}
}

// databaseName gives every bot its own database on the same DATABASE_URL, so several bots
// can share one MongoDB without mixing settings. The first bot keeps "azml" (old data stays);
// another bot gets "azml_<bot id>". DATABASE_NAME overrides it.
func databaseName(ctx context.Context, client *mongo.Client, botToken string) string {
	if n := strings.TrimSpace(os.Getenv("DATABASE_NAME")); n != "" {
		return n
	}
	botID := strings.SplitN(strings.TrimSpace(botToken), ":", 2)[0]
	if botID == "" {
		return "azml"
	}
	col := client.Database("azml").Collection("settings")
	var doc struct {
		Value string `bson:"value"`
	}
	err := col.FindOne(ctx, bson.M{"_id": "_OWNER_BOT_ID"}).Decode(&doc)
	switch {
	case err == nil && doc.Value == botID:
		return "azml"
	case err == nil && doc.Value != "":
		return "azml_" + botID
	}
	// "azml" has no owner yet: the bot that used it so far (or the first one) takes it
	_, _ = col.UpdateOne(ctx, bson.M{"_id": "_OWNER_BOT_ID"},
		bson.M{"$set": bson.M{"value": botID}}, options.Update().SetUpsert(true))
	return "azml"
}
