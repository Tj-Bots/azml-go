package modules

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"azml-go/helper/ext"
	"azml-go/helper/pool"
	"azml-go/helper/telegram"

	tdDownloader "github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

// ---- task flags from our Python bot: -t -sp -h -u -ea -cv -ca -j -st ----

// parseSizeArg reads "1.5GB", "500MB", "2000M", or bytes.
func parseSizeArg(s string) int64 {
	s = strings.ToUpper(strings.TrimSpace(s))
	mult := float64(1)
	for _, u := range []struct {
		suf string
		m   float64
	}{{"GB", 1 << 30}, {"G", 1 << 30}, {"MB", 1 << 20}, {"M", 1 << 20}, {"KB", 1 << 10}, {"K", 1 << 10}} {
		if strings.HasSuffix(s, u.suf) {
			mult = u.m
			s = strings.TrimSuffix(s, u.suf)
			break
		}
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f <= 0 {
		return 0
	}
	return int64(f * mult)
}

var headerKeyRe = regexp.MustCompile(`([A-Za-z][A-Za-z0-9-]*):\s`)

// parseHeaders: "Referer: https://a.com Cookie: a=b" or "Referer: x|Cookie: y".
func parseHeaders(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	if strings.Contains(s, "|") {
		for _, h := range strings.Split(s, "|") {
			if h = strings.TrimSpace(h); strings.Contains(h, ":") {
				out = append(out, h)
			}
		}
		return out
	}
	idx := headerKeyRe.FindAllStringIndex(s, -1)
	for i, loc := range idx {
		end := len(s)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		if h := strings.TrimSpace(s[loc[0]:end]); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// taskHeaders: -h headers plus Basic auth from -u / -p.
func taskHeaders(headers, user, pass string) []string {
	out := parseHeaders(headers)
	if user != "" {
		out = append(out, "Authorization: Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
	}
	return out
}

// fetchTaskThumb downloads the -t thumbnail: an image URL or a t.me message link.
func (m *LeechModule) fetchTaskThumb(ctx context.Context, peer tg.InputPeerClass, link string) string {
	dest := filepath.Join(os.TempDir(), fmt.Sprintf("tthumb_%s.jpg", ext.RandomHex(4)))
	if cid, mid, ok := ext.ParseTelegramLink(link); ok && mid > 0 {
		var p tg.InputPeerClass = peer
		if cid != 0 {
			if h := telegram.ResolveChannelAccessHash(ctx, m.rawAPI, cid); h != 0 {
				p = &tg.InputPeerChannel{ChannelID: pool.RawChannelID(cid), AccessHash: h}
			}
		} else if um := publicMsgLink.FindStringSubmatch(link); um != nil {
			// t.me/<username>/<id>: resolve the public chat
			if res, err := m.rawAPI.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: um[1]}); err == nil {
				for _, c := range res.Chats {
					if ch, ok := c.(*tg.Channel); ok {
						p = &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
					}
				}
			}
		}
		tm, err := telegram.FetchMessage(ctx, m.rawAPI, p, mid)
		if err != nil || tm == nil {
			log.Printf("[-t] can't read %s: %v", link, err)
			return ""
		}
		var loc tg.InputFileLocationClass
		switch md := tm.Media.(type) {
		case *tg.MessageMediaPhoto:
			if ph, ok := md.Photo.(*tg.Photo); ok {
				size := ""
				for _, s := range ph.Sizes {
					switch v := s.(type) {
					case *tg.PhotoSize:
						size = v.Type
					case *tg.PhotoSizeProgressive:
						size = v.Type
					}
				}
				loc = &tg.InputPhotoFileLocation{ID: ph.ID, AccessHash: ph.AccessHash, FileReference: ph.FileReference, ThumbSize: size}
			}
		case *tg.MessageMediaDocument:
			if d, ok := md.Document.(*tg.Document); ok && d.Size < 10<<20 {
				loc = d.AsInputDocumentFileLocation("")
			}
		}
		if loc == nil {
			return ""
		}
		if _, err := tdDownloader.NewDownloader().Download(m.rawAPI, loc).ToPath(ctx, dest); err != nil {
			log.Printf("[-t] download failed: %v", err)
			return ""
		}
		return dest
	}
	if !ext.IsURL(link) {
		return ""
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, "GET", link, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return ""
	}
	f, err := os.Create(dest)
	if err != nil {
		return ""
	}
	_, err = io.Copy(f, io.LimitReader(resp.Body, 10<<20))
	_ = f.Close()
	if err != nil {
		_ = os.Remove(dest)
		return ""
	}
	return dest
}

// mediaFiles lists the files under path (file or folder).
func mediaFiles(path string) []string {
	var out []string
	_ = filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func probeKind(p string) (video, audio bool) {
	d, err := ext.ProbeMedia(p)
	if err != nil || d == nil {
		return false, false
	}
	return d.VideoCodec != "" && d.VideoCodec != "mjpeg" && d.VideoCodec != "png", d.AudioCodec != ""
}

func runFFmpeg(args ...string) error {
	out, err := exec.Command(ext.FFmpegBin(), append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

var audioCodecArgs = map[string][]string{
	"mp3":  {"-c:a", "libmp3lame", "-q:a", "0"},
	"m4a":  {"-c:a", "aac", "-b:a", "256k"},
	"aac":  {"-c:a", "aac", "-b:a", "256k"},
	"flac": {"-c:a", "flac"},
	"opus": {"-c:a", "libopus", "-b:a", "192k"},
	"ogg":  {"-c:a", "libvorbis", "-q:a", "6"},
	"wav":  {"-c:a", "pcm_s16le"},
}

// extractAudio (-ea): every video becomes only its audio.
func extractAudio(path, format string) string {
	if format == "" {
		format = "mp3"
	}
	codec, ok := audioCodecArgs[format]
	if !ok {
		format, codec = "mp3", audioCodecArgs["mp3"]
	}
	for _, f := range mediaFiles(path) {
		v, a := probeKind(f)
		if !v || !a {
			continue
		}
		out := strings.TrimSuffix(f, filepath.Ext(f)) + "." + format
		// m4a from aac: copy without re-encoding
		args := append([]string{"-i", f, "-vn", "-map", "0:a:0"}, codec...)
		if format == "m4a" {
			if d, _ := ext.ProbeMedia(f); d != nil && d.AudioCodec == "aac" {
				args = []string{"-i", f, "-vn", "-map", "0:a:0", "-c:a", "copy"}
			}
		}
		if err := runFFmpeg(append(args, out)...); err == nil {
			_ = os.Remove(f)
			if path == f {
				path = out
			}
		} else {
			log.Printf("[-ea] %s: %v", filepath.Base(f), err)
		}
	}
	return path
}

// convertMedia (-cv / -ca): videos remuxed to cv (copy), audios encoded to ca.
func convertMedia(path, cv, ca string) string {
	for _, f := range mediaFiles(path) {
		cur := strings.TrimPrefix(strings.ToLower(filepath.Ext(f)), ".")
		v, a := probeKind(f)
		var out string
		var args []string
		switch {
		case v && cv != "" && cur != cv:
			out = strings.TrimSuffix(f, filepath.Ext(f)) + "." + cv
			args = []string{"-i", f, "-map", "0", "-c", "copy", out}
		case !v && a && ca != "" && cur != ca:
			codec, ok := audioCodecArgs[ca]
			if !ok {
				continue
			}
			out = strings.TrimSuffix(f, filepath.Ext(f)) + "." + ca
			args = append(append([]string{"-i", f, "-vn", "-map", "0:a"}, codec...), out)
		default:
			continue
		}
		err := runFFmpeg(args...)
		if err != nil && v {
			// some streams can't go into this container: keep video + audio only, re-encode subtitles away
			err = runFFmpeg("-i", f, "-map", "0:v", "-map", "0:a?", "-c", "copy", out)
		}
		if err != nil {
			log.Printf("[-cv/-ca] %s: %v", filepath.Base(f), err)
			_ = os.Remove(out)
			continue
		}
		_ = os.Remove(f)
		if path == f {
			path = out
		}
	}
	return path
}

var publicMsgLink = regexp.MustCompile(`t\.me/([A-Za-z0-9_]{4,})/(\d+)`)

var partRe = regexp.MustCompile(`^(.*)\.(\d{3})$`)

// joinParts (-j): file.mkv.001, .002 ... become file.mkv.
func joinParts(path string) string {
	groups := map[string][]string{}
	for _, f := range mediaFiles(path) {
		if m := partRe.FindStringSubmatch(f); m != nil {
			groups[m[1]] = append(groups[m[1]], f)
		}
	}
	for base, parts := range groups {
		if len(parts) < 2 || ext.IsArchiveSplit(parts[0]) {
			continue // archives are joined by the extractor (-e)
		}
		sort.Strings(parts)
		out, err := os.Create(base)
		if err != nil {
			continue
		}
		ok := true
		for _, p := range parts {
			in, err := os.Open(p)
			if err != nil {
				ok = false
				break
			}
			_, err = io.Copy(out, in)
			_ = in.Close()
			if err != nil {
				ok = false
				break
			}
		}
		_ = out.Close()
		if !ok {
			_ = os.Remove(base)
			continue
		}
		for _, p := range parts {
			_ = os.Remove(p)
		}
		if path == parts[0] {
			path = base
		}
	}
	return path
}

// extractAllStreams (-st): every video, audio and subtitle track as its own file.
func extractAllStreams(path, outDir string) (string, error) {
	_ = os.MkdirAll(outDir, 0755)
	done := 0
	for _, f := range mediaFiles(path) {
		out, err := exec.Command(ext.FFprobeBin(), "-v", "error", "-show_entries", "stream=index,codec_type,codec_name:stream_tags=language", "-of", "csv=p=0", f).Output()
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			fields := strings.Split(line, ",")
			if len(fields) < 3 {
				continue
			}
			idx, codec, kind := fields[0], fields[1], fields[2]
			lang := ""
			if len(fields) > 3 && fields[3] != "" {
				lang = "." + fields[3]
			}
			var extn string
			var cargs []string
			switch kind {
			case "video":
				if codec == "mjpeg" || codec == "png" {
					continue
				}
				extn, cargs = "mkv", []string{"-c", "copy"}
			case "audio":
				extn = "mka"
				switch codec {
				case "aac":
					extn = "m4a"
				case "mp3", "flac", "opus":
					extn = codec
				case "ac3", "eac3", "dts":
					extn = codec
				}
				cargs = []string{"-c", "copy"}
			case "subtitle":
				switch codec {
				case "subrip":
					extn, cargs = "srt", []string{"-c", "copy"}
				case "ass", "ssa":
					extn, cargs = "ass", []string{"-c", "copy"}
				case "webvtt":
					extn, cargs = "vtt", []string{"-c", "copy"}
				default:
					continue // picture subtitles (PGS) can't be a text file
				}
			default:
				continue
			}
			name := fmt.Sprintf("%s.%s%s.%s", base, idx, lang, extn)
			if err := runFFmpeg(append(append([]string{"-i", f, "-map", "0:" + idx}, cargs...), filepath.Join(outDir, name))...); err == nil {
				done++
			} else {
				log.Printf("[-st] stream %s of %s: %v", idx, filepath.Base(f), err)
			}
		}
	}
	if done == 0 {
		return "", fmt.Errorf("no streams extracted")
	}
	return outDir, nil
}
