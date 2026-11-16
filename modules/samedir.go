package modules

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"azml-go/config"
	"azml-go/helper/ext"
)

// -m (same folder) like the Python bot: the tasks of one -i / -b run download into one folder,
// the last one to finish uploads it (with -z: one archive of everything).

type sameDirGroup struct {
	mu    sync.Mutex
	dir   string // <DownloadDir>/samedir_xxx/<name>
	total int
	done  int
}

var (
	sameDirMu     sync.Mutex
	sameDirGroups = map[string]*sameDirGroup{}
)

// newSameDir creates the group for total tasks and returns its key.
func newSameDir(name string, total int) string {
	key := "sd" + ext.RandomHex(4)
	name = strings.TrimSpace(strings.NewReplacer("/", "_", "\\", "_").Replace(name))
	if name == "" {
		name = "Leech Folder"
	}
	g := &sameDirGroup{dir: filepath.Join(config.Get().DownloadDir, "samedir_"+key, name), total: total}
	_ = os.MkdirAll(g.dir, 0777)
	sameDirMu.Lock()
	sameDirGroups[key] = g
	sameDirMu.Unlock()
	return key
}

// sameDirAdd moves a finished download into the group folder (nil path = the task failed).
// It returns the folder when this was the last task, "" otherwise.
func sameDirAdd(key, downloaded string) (string, error) {
	sameDirMu.Lock()
	g := sameDirGroups[key]
	sameDirMu.Unlock()
	if g == nil {
		return "", fmt.Errorf("same folder group not found")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if downloaded != "" {
		dest := filepath.Join(g.dir, filepath.Base(downloaded))
		for i := 1; ; i++ { // keep both when two files have the same name
			if _, err := os.Stat(dest); os.IsNotExist(err) {
				break
			}
			ext := filepath.Ext(downloaded)
			dest = filepath.Join(g.dir, fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(filepath.Base(downloaded), ext), i, ext))
		}
		if err := os.Rename(downloaded, dest); err != nil {
			return "", err
		}
	}
	g.done++
	if g.done < g.total {
		return "", nil
	}
	sameDirMu.Lock()
	delete(sameDirGroups, key)
	sameDirMu.Unlock()
	return g.dir, nil
}
