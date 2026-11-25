package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"azml-go/config"
	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

// Video Tools (-vt) like the Python bot: the user picks one tool (plus Rename) before the task
// starts; everything is stream copy, nothing is re-encoded. VT_CUSTOM_CMDS adds owner tools,
// DISABLE_VT turns the menu off.

const vtTimeout = 180 * time.Second

var (
	vtExtract = [][2]string{{"audio", "Audio"}, {"subs", "Subtitles"}, {"video", "Video Only"}, {"all", "Everything"}}
	vtRemove  = [][2]string{{"subs", "Subtitles"}, {"audio", "Extra Audio"}, {"attach", "Attachments"}}
	vtConvert = [][2]string{{"mkv", "MKV"}, {"mp4", "MP4"}, {"mp3", "MP3 (Audio)"}, {"m4a", "M4A (Audio)"}}
	vtShots   = []int{3, 5, 10}
	vtChapter = [][2]string{{"remove", "Remove All"}, {"5", "Every 5 Min"}, {"10", "Every 10 Min"}, {"15", "Every 15 Min"}}
	vtASplit  = [][2]string{{"10", "10 Min Parts"}, {"30", "30 Min Parts"}, {"60", "60 Min Parts"}}
	vtLabels  = map[string]string{"rename": "Rename", "merge_video": "Video + Video", "merge_audio": "Video + Audio",
		"merge_subs": "Video + Subtitle", "extract": "Extract", "remove": "Remove Stream", "trim": "Trim",
		"ss": "Screenshots", "convert": "Convert To"}
)

// VTSelection is what the user chose.
type VTSelection struct {
	Rename     string
	MergeVideo bool
	MergeAudio bool
	MergeSubs  bool
	Extract    string // audio | subs | video | all
	Remove     string // subs | audio | attach
	TrimStart  string
	TrimEnd    string
	Shots      int
	Convert    string
	Custom     string // name of an owner tool
	MergeAll   bool   // video + audio + subtitle files
	StripMeta  bool
	AudioDelay string // seconds, can be negative
	SubDelay   string // seconds, can be negative
	Chapters   string // remove | minutes
	DefAudio   string // "ask" (the user picks a track after the download), a track number or a language
	DefSubs    string // the same for the default subtitle
	AudioSplit string // minutes
	Translate  string // language code
}

func (s *VTSelection) empty() bool {
	return *s == VTSelection{}
}

// clearTool: one tool at a time (Rename stays)
func (s *VTSelection) clearTool() {
	r := s.Rename
	*s = VTSelection{Rename: r}
}

type vtState struct {
	mu     sync.Mutex
	user   int64
	chat   int64
	msgID  int
	peer   tg.InputPeerClass
	sel    VTSelection
	page   string // main | extract | remove | convert | ss | chapters | asplit
	wait   string // rename | trim | async | ssync | daudio | translate
	start  time.Time
	result chan *VTSelection // nil = cancelled
	once   sync.Once
}

var vtStates sync.Map // "uid" -> *vtState

func vtCustomTools() (names []string, cmds map[string]string) {
	cmds = map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(config.Get().VtCustomCmds, `\n`, "\n"), "\n") {
		name, cmd, ok := strings.Cut(line, ":")
		name, cmd = strings.TrimSpace(name), strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(cmd), "ffmpeg"))
		if ok && name != "" && cmd != "" {
			if _, dup := cmds[name]; !dup {
				names = append(names, name)
			}
			cmds[name] = cmd
		}
	}
	return names, cmds
}

func vtSelectedText(s VTSelection) []string {
	var out []string
	if s.Rename != "" {
		out = append(out, "Rename → <code>"+telegram.EscapeHTML(s.Rename)+"</code>")
	}
	if s.MergeVideo {
		out = append(out, vtLabels["merge_video"])
	}
	if s.MergeAudio {
		out = append(out, vtLabels["merge_audio"])
	}
	if s.MergeSubs {
		out = append(out, vtLabels["merge_subs"])
	}
	name := func(list [][2]string, k string) string {
		for _, x := range list {
			if x[0] == k {
				return x[1]
			}
		}
		return k
	}
	if s.Extract != "" {
		out = append(out, "Extract → "+name(vtExtract, s.Extract))
	}
	if s.Remove != "" {
		out = append(out, "Remove Stream → "+name(vtRemove, s.Remove))
	}
	if s.TrimStart != "" {
		t := s.TrimStart
		if s.TrimEnd != "" {
			t += " - " + s.TrimEnd
		}
		out = append(out, "Trim → "+telegram.EscapeHTML(t))
	}
	if s.Shots > 0 {
		out = append(out, fmt.Sprintf("Screenshots → %d", s.Shots))
	}
	if s.Convert != "" {
		out = append(out, "Convert To → "+name(vtConvert, s.Convert))
	}
	if s.Custom != "" {
		out = append(out, telegram.EscapeHTML(s.Custom))
	}
	if s.MergeAll {
		out = append(out, "Video + Audio + Sub")
	}
	if s.StripMeta {
		out = append(out, "Strip Metadata")
	}
	if s.AudioDelay != "" {
		out = append(out, "Audio Sync → "+telegram.EscapeHTML(s.AudioDelay)+"s")
	}
	if s.SubDelay != "" {
		out = append(out, "Subtitle Sync → "+telegram.EscapeHTML(s.SubDelay)+"s")
	}
	if s.Chapters != "" {
		out = append(out, "Chapters → "+name(vtChapter, s.Chapters))
	}
	if s.DefAudio != "" {
		out = append(out, "Default Audio → "+vtPickText(s.DefAudio))
	}
	if s.DefSubs != "" {
		out = append(out, "Default Subtitle → "+vtPickText(s.DefSubs))
	}
	if s.AudioSplit != "" {
		out = append(out, "Audio Split → "+name(vtASplit, s.AudioSplit))
	}
	if s.Translate != "" {
		out = append(out, "Translate Subtitles → "+telegram.EscapeHTML(s.Translate))
	}
	return out
}

func (st *vtState) btn(b *telegram.ButtonMaker, action, label string, on bool, arg string) {
	data := strings.TrimSpace(fmt.Sprintf("vtool %d %s %s", st.user, action, arg))
	if on {
		b.IButtonStyled("✓ "+label, data, "success")
	} else {
		b.IButton(label, data)
	}
}

func (st *vtState) menu() (string, *telegram.ButtonMaker) {
	b := telegram.NewButtonMaker()
	back := func() {
		b.Row()
		b.IButtonStyled("◀️", fmt.Sprintf("vtool %d back", st.user), "primary")
		b.Row()
	}
	switch st.wait {
	case "rename":
		if st.sel.Rename != "" {
			b.IButton("Remove", fmt.Sprintf("vtool %d unset rename", st.user))
		}
		back()
		return "㊂ <b><u>Video Tools : Rename</u></b>\n\n<blockquote>Send the new name.\nWithout extension the original one is kept.</blockquote>", b
	case "trim":
		if st.sel.TrimStart != "" {
			b.IButton("Remove", fmt.Sprintf("vtool %d unset trim", st.user))
		}
		back()
		return "㊂ <b><u>Video Tools : Trim</u></b>\n\n<blockquote>Send start and end:\n<code>00:01:30-00:05:00</code> or <code>90-300</code> (seconds)\nOnly start: <code>00:10:00</code></blockquote>\n<i>Cut without re-encoding, starts at the nearest keyframe.</i>", b
	}
	if p, ok := vtPrompts[st.wait]; ok {
		if p.isSet(&st.sel) {
			b.IButton("Remove", fmt.Sprintf("vtool %d unset %s", st.user, st.wait))
		}
		back()
		return p.text, b
	}
	grid2 := func(items [][2]string, action, cur string) {
		for i, it := range items {
			st.btn(b, action, it[1], cur == it[0], it[0])
			if i%2 == 1 {
				b.Row()
			}
		}
		back()
	}
	switch st.page {
	case "extract":
		grid2(vtExtract, "extract", st.sel.Extract)
		return "㊂ <b><u>Video Tools : Extract</u></b>\n\n<i>Choose one, it takes you back to the menu.</i>", b
	case "remove":
		grid2(vtRemove, "remove", st.sel.Remove)
		return "㊂ <b><u>Video Tools : Remove Stream</u></b>\n\n<i>Choose one, it takes you back to the menu.</i>", b
	case "convert":
		grid2(vtConvert, "convert", st.sel.Convert)
		return "㊂ <b><u>Video Tools : Convert To</u></b>\n\n<i>Choose one, it takes you back to the menu.</i>", b
	case "chapters":
		grid2(vtChapter, "chapters", st.sel.Chapters)
		return "㊂ <b><u>Video Tools : Chapters</u></b>\n\n<i>Remove the chapters, or add one every few minutes. Choose one, it takes you back to the menu.</i>", b
	case "asplit":
		grid2(vtASplit, "asplit", st.sel.AudioSplit)
		return "㊂ <b><u>Video Tools : Audio Split</u></b>\n\n<i>Cuts audio files into parts. Choose one, it takes you back to the menu.</i>", b
	case "ss":
		var items [][2]string
		for _, n := range vtShots {
			items = append(items, [2]string{strconv.Itoa(n), fmt.Sprintf("%d Shots", n)})
		}
		grid2(items, "ss", strconv.Itoa(st.sel.Shots))
		return "㊂ <b><u>Video Tools : Screenshots</u></b>\n\n<i>Choose one, it takes you back to the menu.</i>", b
	}
	st.btn(b, "rename", "Rename", st.sel.Rename != "", "")
	b.Row()
	st.btn(b, "merge_video", "Video + Video", st.sel.MergeVideo, "")
	st.btn(b, "merge_audio", "Video + Audio", st.sel.MergeAudio, "")
	b.Row()
	st.btn(b, "merge_subs", "Video + Subtitle", st.sel.MergeSubs, "")
	st.btn(b, "page", "Extract", st.sel.Extract != "", "extract")
	b.Row()
	st.btn(b, "page", "Remove Stream", st.sel.Remove != "", "remove")
	st.btn(b, "trim", "Trim", st.sel.TrimStart != "", "")
	b.Row()
	st.btn(b, "page", "Screenshots", st.sel.Shots > 0, "ss")
	st.btn(b, "page", "Convert To", st.sel.Convert != "", "convert")
	b.Row()
	st.btn(b, "merge_all", "Video + Audio + Sub", st.sel.MergeAll, "")
	st.btn(b, "strip", "Strip Metadata", st.sel.StripMeta, "")
	b.Row()
	st.btn(b, "async", "Audio Sync", st.sel.AudioDelay != "", "")
	st.btn(b, "ssync", "Subtitle Sync", st.sel.SubDelay != "", "")
	b.Row()
	st.btn(b, "daudio", "Default Audio", st.sel.DefAudio != "", "")
	st.btn(b, "dsubs", "Default Subtitle", st.sel.DefSubs != "", "")
	b.Row()
	st.btn(b, "page", "Chapters", st.sel.Chapters != "", "chapters")
	st.btn(b, "page", "Audio Split", st.sel.AudioSplit != "", "asplit")
	b.Row()
	st.btn(b, "translate", "Translate Subtitles", st.sel.Translate != "", "")
	b.Row()
	names, _ := vtCustomTools()
	for i, n := range names {
		st.btn(b, "custom", n, st.sel.Custom == n, strconv.Itoa(i))
		if i%2 == 1 {
			b.Row()
		}
	}
	b.Row()
	chosen := vtSelectedText(st.sel)
	if len(chosen) > 0 {
		b.IButtonStyled("Reset", fmt.Sprintf("vtool %d reset", st.user), "danger")
		b.Row()
		b.IButtonStyled("❌", fmt.Sprintf("vtool %d close", st.user), "danger")
		b.IButtonStyled("Continue ▶", fmt.Sprintf("vtool %d done", st.user), "success")
	} else {
		b.IButtonStyled("❌", fmt.Sprintf("vtool %d close", st.user), "danger")
	}
	b.Row()
	lines := "<b>╭</b> <b>Selected:</b> None"
	if len(chosen) > 0 {
		lines = "<b>╭</b> <b>Selected:</b>\n┠ " + strings.Join(chosen, "\n┠ ")
	}
	left := int((vtTimeout - time.Since(st.start)).Seconds())
	if left < 0 {
		left = 0
	}
	text := fmt.Sprintf("㊂ <b><u>Video Tools</u></b>\n\n<blockquote>%s\n<b>╰</b> <b>Closes in:</b> %ds</blockquote>\n\n<i>Pick one tool, Rename can be added to it. Nothing is re-encoded.</i>", lines, left)
	return text, b
}

func (m *LeechModule) vtRefresh(st *vtState) {
	text, b := st.menu()
	_ = m.sender.EditMessageWithButtons(context.Background(), st.peer, st.msgID, text, b, 2)
}

// videoToolsMenu shows the menu and waits; nil = cancelled.
func (m *LeechModule) videoToolsMenu(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) *VTSelection {
	st := &vtState{user: msg.UserID, chat: msg.ChatID, peer: peer, page: "main", start: time.Now(), result: make(chan *VTSelection, 1)}
	text, b := st.menu()
	id, err := m.sender.SendMessageWithButtons(ctx, peer, text, msg.MessageID, b, 2)
	if err != nil || id <= 0 {
		return &VTSelection{}
	}
	st.msgID = id
	key := strconv.FormatInt(msg.UserID, 10)
	vtStates.Store(key, st)
	defer func() {
		vtStates.Delete(key)
		if s, ok := ext.GetPendingSettingSession(msg.UserID, msg.ChatID); ok && s != nil && strings.HasPrefix(s.Key, "vt_") {
			ext.ClearPendingSettingSession(msg.UserID)
		}
		_ = m.sender.DeleteMessage(context.Background(), peer, []int{id})
	}()
	select {
	case r := <-st.result:
		return r
	case <-time.After(vtTimeout): // timeout: go on with what was picked
		st.mu.Lock()
		defer st.mu.Unlock()
		s := st.sel
		return &s
	case <-ctx.Done():
		return nil
	}
}

// HandleVTCallback: "vtool <uid> <action> [arg]".
func (m *LeechModule) HandleVTCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
	parts := strings.Fields(data)
	if len(parts) < 3 {
		return
	}
	if parts[2] == "pick" { // "vtool <uid> pick <token> <n|skip>": a track after the download
		m.handleVTPick(ctx, peer, queryID, msgID, parts, fromUID)
		return
	}
	v, ok := vtStates.Load(parts[1])
	if !ok {
		_ = m.sender.AnswerCallback(ctx, queryID, "This menu is closed!", true)
		_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
		return
	}
	st := v.(*vtState)
	if fromUID != st.user || msgID != st.msgID {
		_ = m.sender.AnswerCallback(ctx, queryID, "𝙏𝙝𝙖𝙩'𝙨 𝙉𝙤𝙩 𝙔𝙤𝙪𝙧𝙨...😤", true)
		return
	}
	_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	action, arg := parts[2], ""
	if len(parts) > 3 {
		arg = parts[3]
	}
	st.mu.Lock()
	finish := func(r *VTSelection) {
		st.once.Do(func() { st.result <- r })
	}
	switch action {
	case "close":
		st.mu.Unlock()
		finish(nil)
		return
	case "done":
		s := st.sel
		st.mu.Unlock()
		finish(&s)
		return
	case "reset":
		st.sel = VTSelection{}
	case "back":
		st.wait, st.page = "", "main"
		if s, ok := ext.GetPendingSettingSession(st.user, st.chat); ok && s != nil && strings.HasPrefix(s.Key, "vt_") {
			ext.ClearPendingSettingSession(st.user)
		}
	case "page":
		st.page = arg
	case "rename", "trim":
		st.wait = action
		ext.SetPendingSettingSession(st.user, "vt_"+action, st.chat, st.msgID, vtTimeout)
	case "daudio", "dsubs": // the tracks are known only after the download: the user picks one then
		was := (action == "daudio" && st.sel.DefAudio != "") || (action == "dsubs" && st.sel.DefSubs != "")
		st.sel.clearTool()
		if !was {
			if action == "daudio" {
				st.sel.DefAudio = "ask"
			} else {
				st.sel.DefSubs = "ask"
			}
		}
	case "async", "ssync", "translate":
		st.wait = action
		ext.SetPendingSettingSession(st.user, "vt_"+action, st.chat, st.msgID, vtTimeout)
	case "merge_all", "strip":
		was := map[string]bool{"merge_all": st.sel.MergeAll, "strip": st.sel.StripMeta}[action]
		st.sel.clearTool()
		if !was {
			if action == "merge_all" {
				st.sel.MergeAll = true
			} else {
				st.sel.StripMeta = true
			}
		}
	case "chapters", "asplit":
		cur := map[string]string{"chapters": st.sel.Chapters, "asplit": st.sel.AudioSplit}[action]
		st.sel.clearTool()
		if cur != arg {
			if action == "chapters" {
				st.sel.Chapters = arg
			} else {
				st.sel.AudioSplit = arg
			}
		}
		st.page = "main"
	case "unset":
		switch arg {
		case "rename":
			st.sel.Rename = ""
		case "trim":
			st.sel.TrimStart, st.sel.TrimEnd = "", ""
		default:
			if p, ok := vtPrompts[arg]; ok {
				p.set(&st.sel, "")
			}
		}
		st.wait = ""
	case "merge_video", "merge_audio", "merge_subs":
		was := map[string]bool{"merge_video": st.sel.MergeVideo, "merge_audio": st.sel.MergeAudio, "merge_subs": st.sel.MergeSubs}[action]
		st.sel.clearTool()
		if !was {
			switch action {
			case "merge_video":
				st.sel.MergeVideo = true
			case "merge_audio":
				st.sel.MergeAudio = true
			default:
				st.sel.MergeSubs = true
			}
		}
	case "extract", "remove", "convert", "ss":
		cur := map[string]string{"extract": st.sel.Extract, "remove": st.sel.Remove, "convert": st.sel.Convert, "ss": strconv.Itoa(st.sel.Shots)}[action]
		st.sel.clearTool()
		if cur != arg {
			switch action {
			case "extract":
				st.sel.Extract = arg
			case "remove":
				st.sel.Remove = arg
			case "convert":
				st.sel.Convert = arg
			case "ss":
				st.sel.Shots, _ = strconv.Atoi(arg)
			}
		}
		st.page = "main"
	case "custom":
		names, _ := vtCustomTools()
		if i, err := strconv.Atoi(arg); err == nil && i >= 0 && i < len(names) {
			was := st.sel.Custom == names[i]
			st.sel.clearTool()
			if !was {
				st.sel.Custom = names[i]
			}
		}
	}
	st.mu.Unlock()
	m.vtRefresh(st)
}

// HandleVTInput gets the text for Rename / Trim (true when it was for the menu).
func (m *LeechModule) HandleVTInput(uid int64, key, val string) bool {
	v, ok := vtStates.Load(strconv.FormatInt(uid, 10))
	if !ok {
		return false
	}
	st := v.(*vtState)
	st.mu.Lock()
	switch key {
	case "vt_rename":
		st.sel.Rename = strings.ReplaceAll(strings.TrimSpace(val), "/", "_")
	case "vt_trim":
		start, end, _ := strings.Cut(strings.TrimSpace(val), "-")
		st.sel.clearTool()
		st.sel.TrimStart, st.sel.TrimEnd = strings.TrimSpace(start), strings.TrimSpace(end)
	default:
		if p, ok := vtPrompts[strings.TrimPrefix(key, "vt_")]; ok {
			v, good := p.parse(strings.TrimSpace(val))
			if !good {
				st.mu.Unlock()
				ext.SetPendingSettingSession(uid, key, st.chat, st.msgID, vtTimeout) // wrong value: wait again
				return true
			}
			st.sel.clearTool()
			p.set(&st.sel, v)
		}
	}
	st.wait = ""
	st.mu.Unlock()
	m.vtRefresh(st)
	return true
}

// ---- the tools (ffmpeg, stream copy) ----

func vtVideos(path string) []string {
	var out []string
	for _, f := range mediaFiles(path) {
		if v, _ := probeKind(f); v {
			out = append(out, f)
		}
	}
	return out
}

// vtReplace runs ffmpeg into out; out replaces src (keeps the new extension).
func vtReplace(src, out string, args []string) bool {
	if err := runFFmpeg(append(args, out)...); err != nil {
		_ = os.Remove(out)
		return false
	}
	_ = os.Remove(src)
	_ = os.Rename(out, strings.TrimSuffix(src, filepath.Ext(src))+filepath.Ext(out))
	return true
}

func vtMergeVideos(path string) {
	videos := vtVideos(path)
	if len(videos) < 2 {
		return
	}
	sort.Strings(videos)
	base, ext := strings.TrimSuffix(videos[0], filepath.Ext(videos[0])), filepath.Ext(videos[0])
	lst := base + ".vt_list.txt"
	var sb strings.Builder
	for _, v := range videos {
		sb.WriteString("file '" + strings.ReplaceAll(v, "'", `'\''`) + "'\n")
	}
	_ = os.WriteFile(lst, []byte(sb.String()), 0644)
	out := base + ".merged" + ext
	err := runFFmpeg("-f", "concat", "-safe", "0", "-i", lst, "-map", "0", "-c", "copy", out)
	_ = os.Remove(lst)
	if err == nil {
		for _, v := range videos {
			_ = os.Remove(v)
		}
	} else {
		_ = os.Remove(out)
	}
}

func vtMuxExtra(path, kind string) {
	files := mediaFiles(path)
	videos := vtVideos(path)
	if len(videos) == 0 {
		return
	}
	isVideo := map[string]bool{}
	for _, v := range videos {
		isVideo[v] = true
	}
	var extras []string
	for _, f := range files {
		if isVideo[f] {
			continue
		}
		low := strings.ToLower(f)
		if kind == "audio" {
			if _, a := probeKind(f); a {
				extras = append(extras, f)
			}
		} else if strings.HasSuffix(low, ".srt") || strings.HasSuffix(low, ".ass") || strings.HasSuffix(low, ".ssa") || strings.HasSuffix(low, ".vtt") || strings.HasSuffix(low, ".sub") {
			extras = append(extras, f)
		}
	}
	if len(extras) == 0 {
		return
	}
	video := videos[0]
	base, ext := strings.TrimSuffix(video, filepath.Ext(video)), filepath.Ext(video)
	args := []string{"-i", video}
	for _, e := range extras {
		args = append(args, "-i", e)
	}
	args = append(args, "-map", "0")
	t := "s"
	if kind == "audio" {
		t = "a"
	}
	for i := range extras {
		args = append(args, "-map", fmt.Sprintf("%d:%s", i+1, t))
	}
	args = append(args, "-c", "copy")
	ok := false
	if kind == "subs" && strings.EqualFold(ext, ".mp4") {
		ok = vtReplace(video, base+".vt"+ext, append(append([]string{}, args...), "-c:s", "mov_text"))
	} else {
		ok = vtReplace(video, base+".vt"+ext, args)
	}
	if !ok && !strings.EqualFold(ext, ".mkv") { // mp4 can't hold every codec, mkv can
		ok = vtReplace(video, base+".vt.mkv", args)
	}
	if ok {
		for _, e := range extras {
			_ = os.Remove(e)
		}
	}
}

func vtExtractStreams(path, kind string) {
	probe := ext.FFprobeBin() // before the loop: ext is a local below
	for _, video := range vtVideos(path) {
		base, ext := strings.TrimSuffix(video, filepath.Ext(video)), filepath.Ext(video)
		out, err := exec.Command(probe, "-v", "error", "-print_format", "json", "-show_streams", video).Output()
		if err != nil {
			continue
		}
		var pr struct {
			Streams []struct {
				Index     int               `json:"index"`
				CodecType string            `json:"codec_type"`
				CodecName string            `json:"codec_name"`
				Tags      map[string]string `json:"tags"`
			} `json:"streams"`
		}
		_ = json.Unmarshal(out, &pr)
		done := false
		audioExt := map[string]string{"aac": "m4a", "mp3": "mp3", "ac3": "ac3", "eac3": "eac3", "opus": "opus", "flac": "flac", "dts": "dts", "vorbis": "ogg", "truehd": "thd"}
		subExt := map[string]string{"subrip": "srt", "ass": "ass", "ssa": "ass", "webvtt": "vtt"}
		for _, s := range pr.Streams {
			tag := ""
			if l := s.Tags["language"]; l != "" {
				tag = "." + l
			}
			switch {
			case s.CodecType == "audio" && (kind == "audio" || kind == "all"):
				e, ok := audioExt[s.CodecName]
				if !ok {
					e = "mka"
				}
				if runFFmpeg("-i", video, "-map", fmt.Sprintf("0:%d", s.Index), "-c", "copy", fmt.Sprintf("%s.%d%s.%s", base, s.Index, tag, e)) == nil {
					done = true
				}
			case s.CodecType == "subtitle" && (kind == "subs" || kind == "all"):
				e, ok := subExt[s.CodecName]
				codec := "copy"
				if s.CodecName == "mov_text" {
					e, codec = "srt", "srt"
				} else if !ok {
					e = "mks"
				}
				if runFFmpeg("-i", video, "-map", fmt.Sprintf("0:%d", s.Index), "-c", codec, fmt.Sprintf("%s.%d%s.%s", base, s.Index, tag, e)) == nil {
					done = true
				}
			}
		}
		if (kind == "video" || kind == "all") && runFFmpeg("-i", video, "-map", "0:v", "-c", "copy", base+".video"+ext) == nil {
			done = true
		}
		if done {
			_ = os.Remove(video)
		}
	}
}

func vtRemoveStreams(path, kind string) {
	for _, video := range vtVideos(path) {
		base, ext := strings.TrimSuffix(video, filepath.Ext(video)), filepath.Ext(video)
		args := []string{"-i", video, "-map", "0"}
		switch kind {
		case "subs":
			args = append(args, "-map", "-0:s")
		case "audio":
			args = append(args, "-map", "-0:a", "-map", "0:a:0?")
		case "attach":
			args = append(args, "-map", "-0:t")
		}
		vtReplace(video, base+".vt"+ext, append(args, "-c", "copy"))
	}
}

func vtTrim(path, start, end string) {
	for _, video := range vtVideos(path) {
		base, ext := strings.TrimSuffix(video, filepath.Ext(video)), filepath.Ext(video)
		args := []string{"-ss", start}
		if end != "" {
			args = append(args, "-to", end)
		}
		args = append(args, "-i", video, "-map", "0", "-c", "copy", "-avoid_negative_ts", "make_zero")
		vtReplace(video, base+".vt"+ext, args)
	}
}

func vtCustom(path, cmd string) {
	for _, video := range vtVideos(path) {
		base, ext := strings.TrimSuffix(video, filepath.Ext(video)), filepath.Ext(video)
		args := append([]string{"-i", video}, strings.Fields(cmd)...)
		vtReplace(video, base+".vt"+ext, args)
	}
}

func vtRename(path, name string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return path
	}
	if !fi.IsDir() {
		if filepath.Ext(name) == "" {
			name += filepath.Ext(path)
		}
		np := filepath.Join(filepath.Dir(path), name)
		if os.Rename(path, np) == nil {
			return np
		}
		return path
	}
	if videos := vtVideos(path); len(videos) == 1 {
		vn := name
		if filepath.Ext(vn) == "" {
			vn += filepath.Ext(videos[0])
		}
		_ = os.Rename(videos[0], filepath.Join(filepath.Dir(videos[0]), vn))
	}
	np := filepath.Join(filepath.Dir(strings.TrimRight(path, "/")), strings.TrimSuffix(name, filepath.Ext(name)))
	if os.Rename(path, np) == nil {
		return np
	}
	return path
}

// runVideoTools: merge, remove, trim, extract, convert, owner tool, rename (like the Python bot).
// Screenshots are sent by the normal upload (it returns how many).
func runVideoTools(path string, s *VTSelection) (string, int) {
	if s == nil || s.empty() {
		return path, 0
	}
	if s.MergeVideo {
		vtMergeVideos(path)
	}
	if s.MergeAudio {
		vtMuxExtra(path, "audio")
	}
	if s.MergeSubs {
		vtMuxExtra(path, "subs")
	}
	if s.Remove != "" {
		vtRemoveStreams(path, s.Remove)
	}
	if s.TrimStart != "" {
		vtTrim(path, s.TrimStart, s.TrimEnd)
	}
	if s.Extract != "" {
		vtExtractStreams(path, s.Extract)
	}
	switch s.Convert {
	case "mp3", "m4a":
		path = extractAudio(path, s.Convert)
	case "mkv", "mp4":
		path = convertMedia(path, s.Convert, "")
	}
	if s.MergeAll {
		vtMuxExtra(path, "audio")
		vtMuxExtra(path, "subs")
	}
	if s.StripMeta {
		vtStripMeta(path)
	}
	if s.AudioDelay != "" {
		vtShift(path, "a", s.AudioDelay)
	}
	if s.SubDelay != "" {
		vtShift(path, "s", s.SubDelay)
	}
	if s.Chapters != "" {
		vtChapters(path, s.Chapters)
	}
	if s.DefAudio != "" && s.DefAudio != "ask" {
		vtDefaultAudio(path, s.DefAudio)
	}
	if s.DefSubs != "" && s.DefSubs != "ask" {
		vtDefaultSubs(path, s.DefSubs)
	}
	if s.AudioSplit != "" {
		vtAudioSplit(path, s.AudioSplit)
	}
	if s.Translate != "" {
		vtTranslateSubs(path, s.Translate)
	}
	if s.Custom != "" {
		if _, cmds := vtCustomTools(); cmds[s.Custom] != "" {
			vtCustom(path, cmds[s.Custom])
		}
	}
	if s.Rename != "" {
		path = vtRename(path, s.Rename)
	}
	return path, s.Shots
}
