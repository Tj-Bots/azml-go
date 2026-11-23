package web

import (
	"fmt"
	"html"
	"path/filepath"
	"sort"
	"strings"

	"azml-go/helper/downloader"
	"azml-go/helper/ext"
)

type TreeNode struct {
	Name     string
	IsDir    bool
	FileID   string
	Size     int64
	Selected bool
	Children []*TreeNode
}

func BuildGDriveHTMLTree(files []downloader.GDriveFile) string {
	root := &TreeNode{Name: "Google Drive", IsDir: true}
	folderMap := map[string]*TreeNode{"": root}

	// Sort folders by depth
	folders := make([]downloader.GDriveFile, 0)
	for _, f := range files {
		if f.IsDir {
			folders = append(folders, f)
		}
	}
	sort.Slice(folders, func(i, j int) bool {
		return strings.Count(folders[i].Path, "/") < strings.Count(folders[j].Path, "/")
	})

	for _, f := range folders {
		fullPath := filepath.ToSlash(filepath.Join(f.Path, f.Name))
		parentPath := filepath.ToSlash(f.Path)
		parent := folderMap[parentPath]
		if parent == nil {
			parent = root
		}
		node := &TreeNode{Name: f.Name, IsDir: true, FileID: f.ID}
		parent.Children = append(parent.Children, node)
		folderMap[fullPath] = node
	}

	for _, f := range files {
		if f.IsDir {
			continue
		}
		parentPath := filepath.ToSlash(f.Path)
		parent := folderMap[parentPath]
		if parent == nil {
			parent = root
		}
		node := &TreeNode{
			Name:     f.Name,
			IsDir:    false,
			FileID:   f.ID,
			Size:     f.Size,
			Selected: f.Selected,
		}
		parent.Children = append(parent.Children, node)
	}

	var sb strings.Builder
	renderHTMLTree(root, &sb, 0, 0)
	return sb.String()
}

func BuildMegaHTMLTree(files []downloader.MegaFileInfo) string {
	root := &TreeNode{Name: "MEGA", IsDir: true}
	folderMap := map[string]*TreeNode{"": root}

	folders := make([]downloader.MegaFileInfo, 0)
	for _, f := range files {
		if f.IsDir {
			folders = append(folders, f)
		}
	}
	sort.Slice(folders, func(i, j int) bool {
		return strings.Count(folders[i].Path, "/") < strings.Count(folders[j].Path, "/")
	})

	for _, f := range folders {
		fullPath := filepath.ToSlash(filepath.Join(f.Path, f.Name))
		parentPath := filepath.ToSlash(f.Path)
		parent := folderMap[parentPath]
		if parent == nil {
			parent = root
		}
		node := &TreeNode{Name: f.Name, IsDir: true, FileID: f.ID}
		parent.Children = append(parent.Children, node)
		folderMap[fullPath] = node
	}

	for _, f := range files {
		if f.IsDir {
			continue
		}
		parentPath := filepath.ToSlash(f.Path)
		parent := folderMap[parentPath]
		if parent == nil {
			parent = root
		}
		node := &TreeNode{
			Name:     f.Name,
			IsDir:    false,
			FileID:   f.ID,
			Size:     f.Size,
			Selected: f.Selected,
		}
		parent.Children = append(parent.Children, node)
	}

	var sb strings.Builder
	renderHTMLTree(root, &sb, 0, 0)
	return sb.String()
}

func BuildTorrentHTMLTree(files []downloader.Aria2File) string {
	root := &TreeNode{Name: "Torrent", IsDir: true}
	for _, f := range files {
		cleanPath := filepath.ToSlash(f.Path)
		parts := strings.Split(cleanPath, "/")
		curr := root
		for i := 0; i < len(parts)-1; i++ {
			p := parts[i]
			var found *TreeNode
			for _, ch := range curr.Children {
				if ch.IsDir && ch.Name == p {
					found = ch
					break
				}
			}
			if found == nil {
				found = &TreeNode{Name: p, IsDir: true}
				curr.Children = append(curr.Children, found)
			}
			curr = found
		}
		fileName := parts[len(parts)-1]
		selected := f.Selected != "false"
		var size int64
		fmt.Sscanf(f.Length, "%d", &size)
		curr.Children = append(curr.Children, &TreeNode{
			Name:     fileName,
			IsDir:    false,
			FileID:   f.Index,
			Size:     size,
			Selected: selected,
		})
	}

	var sb strings.Builder
	renderHTMLTree(root, &sb, 0, 0)
	return sb.String()
}

// natLess: natural order, like a file manager: "E2" before "E10", case-insensitive.
func natLess(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		ca, cb := a[0], b[0]
		if isDigit(ca) && isDigit(cb) {
			na, ra := leadingDigits(a)
			nb, rb := leadingDigits(b)
			ta, tb := strings.TrimLeft(na, "0"), strings.TrimLeft(nb, "0")
			if len(ta) != len(tb) {
				return len(ta) < len(tb)
			}
			if ta != tb {
				return ta < tb
			}
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			a, b = ra, rb
			continue
		}
		if ca != cb {
			return ca < cb
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func leadingDigits(s string) (string, string) {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return s[:i], s[i:]
}

// sortTree: folders first, then files, each in natural order (the web page shows them in order).
func sortTree(n *TreeNode) {
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return natLess(a.Name, b.Name)
	})
	for _, c := range n.Children {
		if c.IsDir {
			sortTree(c)
		}
	}
}

// treeStats: the number of files and their size under a folder.
func treeStats(n *TreeNode) (int, int64) {
	files, size := 0, int64(0)
	for _, c := range n.Children {
		if c.IsDir {
			f, s := treeStats(c)
			files, size = files+f, size+s
		} else {
			files++
			size += c.Size
		}
	}
	return files, size
}

// fileKind: the icon of a file by its extension (video, audio, image, archive, subtitle, other).
func fileKind(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mkv", ".mp4", ".avi", ".mov", ".webm", ".m4v", ".ts", ".wmv", ".flv", ".mpg", ".mpeg", ".3gp", ".m2ts":
		return "video"
	case ".mp3", ".flac", ".m4a", ".aac", ".wav", ".ogg", ".opus", ".wma", ".ac3", ".dts", ".eac3":
		return "audio"
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp", ".tiff":
		return "image"
	case ".zip", ".rar", ".7z", ".tar", ".gz", ".xz", ".bz2", ".iso", ".zst":
		return "archive"
	case ".srt", ".ass", ".ssa", ".vtt", ".sub", ".idx", ".sup":
		return "sub"
	}
	return "file"
}

func renderHTMLTree(node *TreeNode, sb *strings.Builder, depth int, folderCounter int) int {
	if depth == 0 {
		sortTree(node)
	}
	for _, child := range node.Children {
		name := html.EscapeString(child.Name)
		if child.IsDir {
			folderCounter++
			files, size := treeStats(child)
			sb.WriteString(fmt.Sprintf(`<div class="fold open"><div class="fhead"><span class="ic k-folder"><svg><use href="#i-folder"/></svg></span><span class="tx"><span class="nm">%s</span><span class="mt">%d files · %s</span></span><svg class="chev"><use href="#i-chev"/></svg><input class="ck fcb" type="checkbox" name="foldernode_%d" aria-label="select folder"></div><div class="kids">`,
				name, files, ext.GetReadableFileSize(size), folderCounter))
			folderCounter = renderHTMLTree(child, sb, depth+1, folderCounter)
			sb.WriteString(`</div></div>`)
			continue
		}
		checked := ""
		if child.Selected {
			checked = " checked"
		}
		kind := fileKind(child.Name)
		extName := strings.ToUpper(strings.TrimPrefix(filepath.Ext(child.Name), "."))
		if extName == "" || len(extName) > 5 {
			extName = "FILE"
		}
		sb.WriteString(fmt.Sprintf(`<label class="item" data-kind="%s"><span class="ic k-%s"><svg><use href="#i-%s"/></svg><b>%s</b></span><span class="tx"><span class="nm">%s</span><span class="mt">%s</span></span><input class="ck fb" type="checkbox" name="filenode_%s" data-size="%d"%s></label><input type="hidden" value="off" name="filenode_%s">`,
			kind, kind, kind, html.EscapeString(extName), name, ext.GetReadableFileSize(child.Size), html.EscapeString(child.FileID), child.Size, checked, html.EscapeString(child.FileID)))
	}
	return folderCounter
}
