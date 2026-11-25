package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"azml-go/helper/ext"
)

// More video tools, all without re-encoding (stream copy): sync, metadata, chapters, default
// audio, audio split, and subtitle translation (Google Translate).

type vtPrompt struct {
	text  string
	parse func(string) (string, bool)
	set   func(*VTSelection, string)
	isSet func(*VTSelection) bool
}

func parseSeconds(v string) (string, bool) {
	v = strings.TrimSuffix(strings.TrimSpace(strings.ReplaceAll(v, ",", ".")), "s")
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f == 0 || f > 3600 || f < -3600 {
		return "", false
	}
	return strconv.FormatFloat(f, 'f', -1, 64), true
}

var langRe = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z]{2,4})?$`)

var vtPrompts = map[string]vtPrompt{
	"async": {
		text:  "㊂ <b><u>Video Tools : Audio Sync</u></b>\n\n<blockquote>Send how many seconds to move the audio:\n<code>1.5</code> = audio comes later\n<code>-0.8</code> = audio comes earlier</blockquote>\n<i>No re-encoding.</i>",
		parse: parseSeconds,
		set:   func(s *VTSelection, v string) { s.AudioDelay = v },
		isSet: func(s *VTSelection) bool { return s.AudioDelay != "" },
	},
	"ssync": {
		text:  "㊂ <b><u>Video Tools : Subtitle Sync</u></b>\n\n<blockquote>Send how many seconds to move the subtitles:\n<code>2</code> = subtitles come later\n<code>-1.5</code> = subtitles come earlier</blockquote>\n<i>For subtitles inside the video. No re-encoding.</i>",
		parse: parseSeconds,
		set:   func(s *VTSelection, v string) { s.SubDelay = v },
		isSet: func(s *VTSelection) bool { return s.SubDelay != "" },
	},
	"daudio": {
		text: "㊂ <b><u>Video Tools : Default Audio</u></b>\n\n<blockquote>Send the audio track that should play first:\na number: <code>2</code> (the second track)\nor a language: <code>heb</code> / <code>eng</code> / <code>jpn</code></blockquote>\n<i>It becomes the first and default track. No re-encoding.</i>",
		parse: func(v string) (string, bool) {
			if n, err := strconv.Atoi(v); err == nil {
				return v, n >= 1 && n <= 50
			}
			return strings.ToLower(v), langRe.MatchString(v)
		},
		set:   func(s *VTSelection, v string) { s.DefAudio = v },
		isSet: func(s *VTSelection) bool { return s.DefAudio != "" },
	},
	"translate": {
		text:  "㊂ <b><u>Video Tools : Translate Subtitles</u></b>\n\n<blockquote>Send the language code to translate to:\n<code>he</code> Hebrew · <code>en</code> English · <code>ar</code> Arabic · <code>ru</code> Russian · <code>es</code> Spanish</blockquote>\n<i>Subtitle files (.srt) are translated next to the original. A video's first text subtitle is translated and added to it as a new track.</i>",
		parse: func(v string) (string, bool) { return strings.ToLower(v), langRe.MatchString(v) },
		set:   func(s *VTSelection, v string) { s.Translate = v },
		isSet: func(s *VTSelection) bool { return s.Translate != "" },
	},
}

// ---- tools ----

func vtStripMeta(path string) {
	for _, video := range vtVideos(path) {
		base, ext := strings.TrimSuffix(video, filepath.Ext(video)), filepath.Ext(video)
		// global tags and stream titles go, languages stay
		vtReplace(video, base+".vt"+ext, []string{"-i", video, "-map", "0", "-map_metadata:g", "-1",
			"-metadata:s", "title=", "-metadata:s", "handler_name=", "-c", "copy"})
	}
}

// vtShift moves the audio ("a") or subtitle ("s") streams by sec seconds.
func vtShift(path, kind, sec string) {
	for _, video := range vtVideos(path) {
		base, ext := strings.TrimSuffix(video, filepath.Ext(video)), filepath.Ext(video)
		args := []string{"-i", video, "-itsoffset", sec, "-i", video, "-map", "0:v?"}
		if kind == "a" {
			args = append(args, "-map", "1:a", "-map", "0:s?")
		} else {
			args = append(args, "-map", "0:a?", "-map", "1:s")
		}
		args = append(args, "-map", "0:t?", "-map_metadata", "0", "-map_chapters", "0", "-c", "copy")
		vtReplace(video, base+".vt"+ext, args)
	}
}

func probeDuration(p string) float64 {
	out, err := exec.Command(ext.FFprobeBin(), "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", p).Output()
	if err != nil {
		return 0
	}
	d, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	return d
}

func vtChapters(path, mode string) {
	for _, video := range vtVideos(path) {
		base, ext := strings.TrimSuffix(video, filepath.Ext(video)), filepath.Ext(video)
		if mode == "remove" {
			vtReplace(video, base+".vt"+ext, []string{"-i", video, "-map", "0", "-map_chapters", "-1", "-c", "copy"})
			continue
		}
		every, _ := strconv.Atoi(mode)
		dur := probeDuration(video)
		if every <= 0 || dur <= 0 {
			continue
		}
		var sb strings.Builder
		sb.WriteString(";FFMETADATA1\n")
		step := int64(every) * 60000
		total := int64(dur * 1000)
		for i, start := 1, int64(0); start < total; i, start = i+1, start+step {
			end := start + step
			if end > total {
				end = total
			}
			fmt.Fprintf(&sb, "[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=Chapter %d\n", start, end, i)
		}
		meta := base + ".vt_chapters.txt"
		_ = os.WriteFile(meta, []byte(sb.String()), 0644)
		vtReplace(video, base+".vt"+ext, []string{"-i", video, "-i", meta, "-map", "0", "-map_metadata", "0", "-map_chapters", "1", "-c", "copy"})
		_ = os.Remove(meta)
	}
}

type probeStream struct {
	Index     int               `json:"index"`
	CodecType string            `json:"codec_type"`
	CodecName string            `json:"codec_name"`
	Channels  int               `json:"channels"`
	Tags      map[string]string `json:"tags"`
}

func probeStreams(p string) []probeStream {
	out, err := exec.Command(ext.FFprobeBin(), "-v", "error", "-print_format", "json", "-show_streams", p).Output()
	if err != nil {
		return nil
	}
	var pr struct {
		Streams []probeStream `json:"streams"`
	}
	_ = json.Unmarshal(out, &pr)
	return pr.Streams
}

// vtDefaultAudio makes one audio track (number or language) the first and default one.
func vtDefaultAudio(path, want string) {
	for _, video := range vtVideos(path) {
		var audio []probeStream
		for _, s := range probeStreams(video) {
			if s.CodecType == "audio" {
				audio = append(audio, s)
			}
		}
		pick := -1
		if n, err := strconv.Atoi(want); err == nil {
			if n >= 1 && n <= len(audio) {
				pick = n - 1
			}
		} else {
			for i, a := range audio {
				l := strings.ToLower(a.Tags["language"])
				if l != "" && (l == want || strings.HasPrefix(l, want) || strings.HasPrefix(want, l)) {
					pick = i
					break
				}
			}
		}
		if pick < 0 {
			continue
		}
		base, ext := strings.TrimSuffix(video, filepath.Ext(video)), filepath.Ext(video)
		args := []string{"-i", video, "-map", "0:v?", "-map", fmt.Sprintf("0:a:%d", pick)}
		for i := range audio {
			if i != pick {
				args = append(args, "-map", fmt.Sprintf("0:a:%d", i))
			}
		}
		args = append(args, "-map", "0:s?", "-map", "0:t?", "-map_metadata", "0", "-map_chapters", "0", "-c", "copy", "-disposition:a:0", "default")
		for i := 1; i < len(audio); i++ {
			args = append(args, fmt.Sprintf("-disposition:a:%d", i), "0")
		}
		vtReplace(video, base+".vt"+ext, args)
	}
}

// vtDefaultSubs makes one subtitle track (number or language) the default one; the others are
// not default any more (nothing is re-encoded).
func vtDefaultSubs(path, want string) {
	for _, video := range vtVideos(path) {
		var subs []probeStream
		for _, s := range probeStreams(video) {
			if s.CodecType == "subtitle" {
				subs = append(subs, s)
			}
		}
		pick := -1
		if n, err := strconv.Atoi(want); err == nil && n >= 1 && n <= len(subs) {
			pick = n - 1
		} else {
			for i, s := range subs {
				if l := strings.ToLower(s.Tags["language"]); l != "" && (l == want || strings.HasPrefix(l, want)) {
					pick = i
					break
				}
			}
		}
		if pick < 0 {
			continue
		}
		base, ext := strings.TrimSuffix(video, filepath.Ext(video)), filepath.Ext(video)
		args := []string{"-i", video, "-map", "0", "-map_metadata", "0", "-map_chapters", "0", "-c", "copy"}
		for i := range subs {
			d := "0"
			if i == pick {
				d = "default"
			}
			args = append(args, fmt.Sprintf("-disposition:s:%d", i), d)
		}
		vtReplace(video, base+".vt"+ext, args)
	}
}

// vtAudioSplit cuts audio-only files into parts of the given minutes.
func vtAudioSplit(path, minutes string) {
	m, _ := strconv.Atoi(minutes)
	if m <= 0 {
		return
	}
	for _, f := range mediaFiles(path) {
		v, a := probeKind(f)
		if v || !a || probeDuration(f) <= float64(m*60) {
			continue
		}
		base, ext := strings.TrimSuffix(f, filepath.Ext(f)), filepath.Ext(f)
		if runFFmpeg("-i", f, "-map", "0:a", "-c", "copy", "-f", "segment", "-segment_time", strconv.Itoa(m*60),
			"-reset_timestamps", "1", base+".part%03d"+ext) == nil {
			_ = os.Remove(f)
		}
	}
}

// vtTrOpts: how the subtitles are translated (Video Tools → Translate Subtitles).
type vtTrOpts struct {
	Key    string // the user's Gemini key: AI translation; "" = Google Translate
	Stream string // "" = the first text subtitle, "all", or a subtitle number (1 = the first subtitle track)
	Drop   bool   // the translation replaces the original track (else it is added next to it)
}

var textSubCodecs = map[string]bool{"subrip": true, "ass": true, "ssa": true, "mov_text": true, "webvtt": true, "text": true}

// vtTranslateSubs: .srt files get a translated copy next to them; in a video the chosen subtitle
// track(s) are translated and added as new tracks (or replace the originals with Drop).
func vtTranslateSubs(path, lang string, o vtTrOpts) {
	var srts []string
	for _, f := range mediaFiles(path) {
		if strings.EqualFold(filepath.Ext(f), ".srt") {
			srts = append(srts, f)
		}
	}
	if len(srts) > 0 { // subtitle files: a translated copy next to each one
		for _, f := range srts {
			base := strings.TrimSuffix(f, filepath.Ext(f))
			_ = translateSRT(f, base+"."+lang+".srt", lang, o.Key)
		}
		return
	}
	for _, video := range vtVideos(path) {
		var subs []probeStream // every subtitle track, in order
		for _, s := range probeStreams(video) {
			if s.CodecType == "subtitle" {
				subs = append(subs, s)
			}
		}
		var pick []int // ordinal among the subtitle tracks
		switch o.Stream {
		case "all":
			for i, s := range subs {
				if textSubCodecs[s.CodecName] {
					pick = append(pick, i)
				}
			}
		case "":
			for i, s := range subs {
				if textSubCodecs[s.CodecName] {
					pick = append(pick, i)
					break
				}
			}
		default:
			if n, err := strconv.Atoi(o.Stream); err == nil && n >= 1 && n <= len(subs) && textSubCodecs[subs[n-1].CodecName] {
				pick = append(pick, n-1)
			}
		}
		if len(pick) == 0 {
			continue
		}
		base, ext := strings.TrimSuffix(video, filepath.Ext(video)), filepath.Ext(video)
		var translated []string // srt files, one per picked track
		var picked []int
		for k, i := range pick {
			src, dst := fmt.Sprintf("%s.vt_src%d.srt", base, k), fmt.Sprintf("%s.vt_tr%d.srt", base, k)
			if runFFmpeg("-i", video, "-map", fmt.Sprintf("0:s:%d", i), "-c:s", "srt", src) != nil {
				continue
			}
			if translateSRT(src, dst, lang, o.Key) == nil {
				translated = append(translated, dst)
				picked = append(picked, i)
			}
			_ = os.Remove(src)
		}
		if len(translated) == 0 {
			continue
		}
		subCodec := "srt"
		if strings.EqualFold(ext, ".mp4") {
			subCodec = "mov_text"
		}
		args := []string{"-i", video}
		for _, t := range translated {
			args = append(args, "-i", t)
		}
		args = append(args, "-map", "0")
		kept := len(subs)
		if o.Drop { // the originals that were translated go
			for _, i := range picked {
				args = append(args, "-map", fmt.Sprintf("-0:s:%d", i))
			}
			kept -= len(picked)
		}
		for k := range translated {
			args = append(args, "-map", fmt.Sprintf("%d:0", k+1))
		}
		args = append(args, "-c", "copy")
		for k, i := range picked {
			n := kept + k
			srcLang := subs[i].Tags["language"]
			title := "Translated (" + lang + ")"
			if srcLang != "" {
				title = fmt.Sprintf("Translated %s → %s", srcLang, lang)
			}
			args = append(args, fmt.Sprintf("-c:s:%d", n), subCodec,
				fmt.Sprintf("-metadata:s:s:%d", n), "language="+lang,
				fmt.Sprintf("-metadata:s:s:%d", n), "title="+title)
		}
		if !vtReplace(video, base+".vt"+ext, args) && !strings.EqualFold(ext, ".mkv") {
			for i, a := range args {
				if a == "mov_text" {
					args[i] = "srt"
				}
			}
			vtReplace(video, base+".vt.mkv", args)
		}
		for _, t := range translated {
			_ = os.Remove(t)
		}
	}
}

type srtCue struct{ head, text string }

func translateSRT(src, dst, lang, geminiKey string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	body := strings.ReplaceAll(strings.TrimPrefix(string(raw), "\ufeff"), "\r\n", "\n")
	var cues []srtCue
	for _, block := range regexp.MustCompile(`\n{2,}`).Split(strings.TrimSpace(body), -1) {
		lines := strings.Split(block, "\n")
		i := 0
		for i < len(lines) && !strings.Contains(lines[i], "-->") {
			i++
		}
		if i >= len(lines) {
			continue
		}
		cues = append(cues, srtCue{head: strings.Join(lines[:i+1], "\n"), text: strings.Join(lines[i+1:], "\n")})
	}
	if len(cues) == 0 {
		return fmt.Errorf("empty subtitle")
	}
	// AI (the user's Gemini key): the whole subtitle in batches, with context; on any error the
	// lines go to Google Translate below
	out := make([]string, len(cues))
	if geminiKey != "" {
		texts := make([]string, len(cues))
		for i, c := range cues {
			texts[i] = strings.TrimSpace(c.text)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		res, err := ext.GeminiTranslate(ctx, geminiKey, texts, vtLangName(lang))
		cancel()
		if err == nil && len(res) == len(cues) {
			var sb strings.Builder
			for i, c := range cues {
				fmt.Fprintf(&sb, "%s\n%s\n\n", c.head, strings.TrimSpace(res[i]))
			}
			return os.WriteFile(dst, []byte(sb.String()), 0644)
		}
		log.Printf("[VT] Gemini translation failed (%v), using Google Translate", err)
	}
	// batches of cues, one cue per line (its own line breaks kept as " ⏎ ")
	const sep = " ⏎ "
	for start := 0; start < len(cues); {
		end, size := start, 0
		for end < len(cues) && size < 3500 {
			size += len(cues[end].text) + 1
			end++
		}
		var lines []string
		for _, c := range cues[start:end] {
			lines = append(lines, strings.ReplaceAll(strings.TrimSpace(c.text), "\n", sep))
		}
		res, err := googleTranslate(strings.Join(lines, "\n"), lang)
		got := strings.Split(strings.TrimRight(res, "\n"), "\n")
		if err != nil || len(got) != end-start {
			for i := start; i < end; i++ { // line count changed: one cue at a time
				t, e := googleTranslate(lines[i-start], lang)
				if e != nil {
					t = lines[i-start]
				}
				got2 := strings.ReplaceAll(strings.TrimSpace(t), "\n", " ")
				out[i] = got2
			}
		} else {
			copy(out[start:end], got)
		}
		start = end
	}
	var sb strings.Builder
	for i, c := range cues {
		t := strings.TrimSpace(out[i])
		t = regexp.MustCompile(`\s*⏎\s*`).ReplaceAllString(t, "\n")
		fmt.Fprintf(&sb, "%s\n%s\n\n", c.head, t)
	}
	return os.WriteFile(dst, []byte(sb.String()), 0644)
}

var translateClient = &http.Client{Timeout: 40 * time.Second}

func googleTranslate(text, lang string) (string, error) {
	// several Google addresses: when one asks for a captcha (blocked server IP) the next one works
	var lastErr error
	for _, host := range []string{"translate.googleapis.com", "translate.google.com"} {
		res, err := gtxTranslate(host, text, lang)
		if err == nil {
			return res, nil
		}
		lastErr = err
	}
	if res, err := chromeExTranslate(text, lang); err == nil {
		return res, nil
	} else if lastErr == nil {
		lastErr = err
	}
	return "", lastErr
}

func gtxTranslate(host, text, lang string) (string, error) {
	u := "https://" + host + "/translate_a/single?client=gtx&sl=auto&dt=t&tl=" + url.QueryEscape(lang)
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := translateClient.PostForm(u, url.Values{"q": {text}})
		if err != nil {
			lastErr = err
			time.Sleep(time.Second)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var data []any
		if resp.StatusCode != 200 || json.Unmarshal(body, &data) != nil || len(data) == 0 {
			lastErr = fmt.Errorf("translate %s: HTTP %d", host, resp.StatusCode)
			if resp.StatusCode == 429 || resp.StatusCode >= 500 {
				time.Sleep(2 * time.Second)
				continue
			}
			return "", lastErr
		}
		parts, _ := data[0].([]any)
		var sb strings.Builder
		for _, p := range parts {
			if seg, ok := p.([]any); ok && len(seg) > 0 {
				if s, ok := seg[0].(string); ok {
					sb.WriteString(s)
				}
			}
		}
		return sb.String(), nil
	}
	return "", lastErr
}

// chromeExTranslate: the address of Google's Chrome extension, answers [["text","en"]].
func chromeExTranslate(text, lang string) (string, error) {
	u := "https://clients5.google.com/translate_a/t?client=dict-chrome-ex&sl=auto&tl=" + url.QueryEscape(lang)
	resp, err := translateClient.PostForm(u, url.Values{"q": {text}})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var data []any
	if resp.StatusCode != 200 || json.Unmarshal(body, &data) != nil || len(data) == 0 {
		return "", fmt.Errorf("translate clients5: HTTP %d", resp.StatusCode)
	}
	switch x := data[0].(type) {
	case string:
		return x, nil
	case []any:
		if len(x) > 0 {
			if s, ok := x[0].(string); ok {
				return s, nil
			}
		}
	}
	return "", fmt.Errorf("translate clients5: bad answer")
}
