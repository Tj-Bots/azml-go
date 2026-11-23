package web

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"unicode"

	"azml-go/config"
	"azml-go/helper/downloader"
)

func StartWebServer() {
	cfg := config.Get()
	port := cfg.BaseURLPort
	if pStr := os.Getenv("PORT"); pStr != "" {
		if p, err := strconv.Atoi(strings.TrimSpace(pStr)); err == nil && p > 0 {
			port = p
		}
	}
	if port <= 0 {
		port = 23434
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/app/files/", handleFiles)

	addr := fmt.Sprintf(":%d", port)
	log.Printf("[WEB SERVER] Starting Base URL web selector server on port %d...", port)
	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil && err != http.ErrServerClosed {
			log.Printf("[WEB SERVER] Server error: %v", err)
		}
	}()
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(homepageHTML))
}

func handleFiles(w http.ResponseWriter, r *http.Request) {
	// Path is /app/files/{id}
	id := strings.TrimPrefix(r.URL.Path, "/app/files/")
	id = strings.TrimSpace(id)
	if id == "" {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	// Compute PIN code: first 4 digits of ID
	pinCode := ""
	for _, ch := range id {
		if unicode.IsDigit(ch) {
			pinCode += string(ch)
			if len(pinCode) == 4 {
				break
			}
		}
	}
	if len(pinCode) < 4 {
		pinCode = "0000"
	}

	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		var selectedIDs []string
		for key, vals := range r.PostForm {
			if strings.HasPrefix(key, "filenode_") && len(vals) > 0 && vals[0] == "on" {
				nodeID := strings.TrimPrefix(key, "filenode_")
				selectedIDs = append(selectedIDs, nodeID)
			}
		}

		if GDriveStore.GetData(id) != nil {
			GDriveStore.SaveSelection(id, selectedIDs)
			log.Printf("[WEB GDRIVE] GID=%s saved %d selected files", id, len(selectedIDs))
		} else if MegaStore.GetData(id) != nil {
			MegaStore.SaveSelection(id, selectedIDs)
			log.Printf("[WEB MEGA] GID=%s saved %d selected files", id, len(selectedIDs))
		} else {
			// Aria2c torrent
			aria := downloader.GetAria2Client()
			if aria != nil {
				resumeList := strings.Join(selectedIDs, ",")
				_ = aria.ChangeOption(id, map[string]interface{}{"select-file": resumeList})
				log.Printf("[WEB TJURL] GID=%s updated select-file: %s", id, resumeList)
			}
		}

		http.Redirect(w, r, fmt.Sprintf("/app/files/%s?pin_code=%s&saved=1", id, pinCode), http.StatusSeeOther)
		return
	}

	// Check PIN code from query param
	userPin := r.URL.Query().Get("pin_code")
	if userPin == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		codePage := strings.ReplaceAll(codePageHTML, "{form_url}", fmt.Sprintf("/app/files/%s", id))
		_, _ = w.Write([]byte(codePage))
		return
	}

	if userPin != pinCode {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`<h1 style="color:red;text-align:center;margin-top:20vh;font-family:sans-serif;">× Incorrect Security PIN Code</h1>`))
		return
	}

	// Build content tree
	contentHTML := ""
	if gdFiles := GDriveStore.GetData(id); gdFiles != nil {
		contentHTML = BuildGDriveHTMLTree(gdFiles)
	} else if megaFiles := MegaStore.GetData(id); megaFiles != nil {
		contentHTML = BuildMegaHTMLTree(megaFiles)
	} else {
		// Try Aria2c
		aria := downloader.GetAria2Client()
		if aria != nil {
			status, err := aria.TellStatus(id)
			if err == nil && len(status.Files) > 0 {
				contentHTML = BuildTorrentHTMLTree(status.Files)
			}
		}
	}

	if contentHTML == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<h1 style="color:#a855f7;text-align:center;margin-top:20vh;font-family:sans-serif;">⌬ No files found or session expired for ID: ` + id + `</h1>`))
		return
	}

	formURL := fmt.Sprintf("/app/files/%s?pin_code=%s", id, pinCode)
	pageRendered := strings.ReplaceAll(pageHTML, "{My_content}", contentHTML)
	pageRendered = strings.ReplaceAll(pageRendered, "{form_url}", formURL)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(pageRendered))
}

const homepageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <style>*{-webkit-tap-highlight-color:transparent}</style>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Leech Bot</title>
    <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.5.1/css/all.min.css">
    <link href="https://fonts.googleapis.com/css2?family=Exo:wght@300;400;600;700&display=swap" rel="stylesheet">
    <style>
        body {
            font-family: 'Exo', sans-serif !important;
            background-color: #0a0f1c;
            color: #ffffff;
            margin: 0;
            padding: 0;
            display: flex;
            align-items: center;
            justify-content: center;
            min-height: 100vh;
        }
        .container {
            text-align: center;
            padding: 2.5rem;
            max-width: 500px;
            background: rgba(16, 24, 48, 0.7);
            border-radius: 20px;
            box-shadow: 0 0 30px rgba(0, 183, 255, 0.2);
            border: 1px solid rgba(0, 183, 255, 0.3);
            backdrop-filter: blur(15px);
        }
        .logo {
            width: 100px;
            height: 100px;
            border-radius: 50%;
            border: 2px solid #00b7ff;
            box-shadow: 0 0 20px rgba(0, 183, 255, 0.4);
            margin-bottom: 1rem;
        }
        .version {
            display: inline-block;
            background: rgba(0, 183, 255, 0.2);
            color: #00b7ff;
            padding: 4px 12px;
            border-radius: 20px;
            font-size: 0.8rem;
            font-weight: bold;
            margin-bottom: 0.5rem;
        }
        h1 {
            font-size: 2.5rem;
            margin: 0.5rem 0;
            letter-spacing: 2px;
            color: #00b7ff;
        }
        .subtitle {
            color: #94a3b8;
            font-size: 1rem;
            margin-bottom: 2rem;
        }
        .links {
            display: flex;
            flex-direction: column;
            gap: 1rem;
        }
        .button {
            display: flex;
            align-items: center;
            justify-content: center;
            gap: 0.75rem;
            padding: 0.9rem;
            background: linear-gradient(45deg, rgba(0, 183, 255, 0.2), rgba(168, 85, 247, 0.2));
            color: #ffffff;
            text-decoration: none;
            border-radius: 12px;
            font-weight: bold;
            transition: all 0.3s ease;
            border: 1px solid rgba(255, 255, 255, 0.1);
        }
        /* only on devices with a real mouse: on phones :hover stays after the tap */
        @media (hover: hover) {
            .button:hover {
                transform: translateY(-2px);
                background: linear-gradient(45deg, #00b7ff, #a855f7);
                color: #000;
                box-shadow: 0 5px 20px rgba(0, 183, 255, 0.4);
            }
        }
        .button:active {
            background: linear-gradient(45deg, #00b7ff, #a855f7);
            color: #000;
        }
        .copyright {
            margin-top: 2rem;
            font-size: 0.8rem;
            color: #64748b;
        }
    </style>
</head>
<body>
    <div class="container">
        <img class="logo" src="https://raw.githubusercontent.com/Tj-Bots/Leech-TJ/wzgram/docs/logo.png" alt="Logo">
        <h1>Leech Bot</h1>
        <p class="subtitle">Fast Telegram Leech &amp; Mirror bot</p>
        <div class="links">
            <a href="https://t.me/TJ_Bots" target="_blank" class="button">
                <i class="fab fa-telegram-plane"></i> Telegram Channel
            </a>
        </div>
        <p class="copyright">&copy; TJ Bots</p>
    </div>
</body>
</html>`

const codePageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="theme-color" content="#07070c">
<title>Enter PIN</title>
<link href="https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@500;700;800&display=swap" rel="stylesheet">
<style>
*{box-sizing:border-box;-webkit-tap-highlight-color:transparent}
body{margin:0;min-height:100vh;display:grid;place-items:center;padding:20px;background:#07070c;color:#f5f5fa;font-family:"Plus Jakarta Sans",system-ui,sans-serif;overflow:hidden}
.aur{position:fixed;inset:0;z-index:-1;filter:blur(80px);opacity:.5}.aur i{position:absolute;border-radius:50%;width:50vw;height:50vw;animation:f 12s ease-in-out infinite alternate}
.aur i:first-child{background:#7c3aed;left:-10vw;top:-10vh}.aur i:last-child{background:#0891b2;right:-10vw;bottom:-10vh;animation-delay:-6s}
@keyframes f{to{transform:translate(8vw,6vh) scale(1.2)}}
.card{width:100%;max-width:380px;padding:30px 24px;border-radius:28px;background:rgba(255,255,255,.05);border:1px solid rgba(255,255,255,.08);backdrop-filter:blur(20px);text-align:center;animation:in .5s cubic-bezier(.2,.9,.2,1)}
@keyframes in{from{opacity:0;transform:translateY(16px) scale(.98)}}
.lock{width:64px;height:64px;margin:0 auto 16px;border-radius:20px;display:grid;place-items:center;background:linear-gradient(135deg,#7c3aed,#06b6d4);box-shadow:0 10px 30px #7c3aed55}
.lock svg{width:30px;height:30px;fill:none;stroke:#fff;stroke-width:2;stroke-linecap:round}
h1{margin:0 0 6px;font-size:22px;font-weight:800}p{margin:0 0 22px;color:#8b8ba3;font-size:14px}
.pins{display:flex;gap:10px;justify-content:center;margin-bottom:20px}
.pins input{width:56px;height:64px;border-radius:16px;border:1px solid rgba(255,255,255,.1);background:rgba(0,0,0,.35);color:#fff;text-align:center;font:800 26px "Plus Jakarta Sans",sans-serif;outline:0;transition:border-color .2s,box-shadow .2s,transform .15s}
.pins input:focus{border-color:#7c3aed;box-shadow:0 0 0 4px #7c3aed33;transform:translateY(-2px)}
button{width:100%;border:0;border-radius:18px;padding:16px;font:800 16px "Plus Jakarta Sans",sans-serif;color:#fff;background:linear-gradient(135deg,#7c3aed,#4f46e5 55%,#06b6d4);box-shadow:0 10px 30px #6d28d966;cursor:pointer}
button:active{transform:scale(.97)}
</style>
</head>
<body>
<div class="aur"><i></i><i></i></div>
<form class="card" action="{form_url}" id="f">
  <div class="lock"><svg viewBox="0 0 24 24"><rect x="5" y="11" width="14" height="10" rx="3"/><path d="M8 11V8a4 4 0 018 0v3"/></svg></div>
  <h1>Enter your PIN</h1>
  <p>Press <b>Pincode</b> in Telegram to see it</p>
  <div class="pins"><input inputmode="numeric" maxlength="1"><input inputmode="numeric" maxlength="1"><input inputmode="numeric" maxlength="1"><input inputmode="numeric" maxlength="1"></div>
  <input type="hidden" name="pin_code" id="pin">
  <button type="submit">Open files</button>
</form>
<script>
var ins=[].slice.call(document.querySelectorAll('.pins input'));ins[0].focus();
ins.forEach(function(el,i){
  el.addEventListener('input',function(){el.value=el.value.replace(/\D/g,'').slice(-1);if(el.value&&ins[i+1])ins[i+1].focus();if(ins.every(function(x){return x.value}))document.getElementById('f').requestSubmit()});
  el.addEventListener('keydown',function(e){if(e.key==='Backspace'&&!el.value&&ins[i-1])ins[i-1].focus()});
  el.addEventListener('paste',function(e){var d=(e.clipboardData.getData('text')||'').replace(/\D/g,'').slice(0,4);if(d.length){e.preventDefault();d.split('').forEach(function(c,k){if(ins[k])ins[k].value=c});if(d.length===4)document.getElementById('f').requestSubmit()}});
});
document.getElementById('f').addEventListener('submit',function(){document.getElementById('pin').value=ins.map(function(x){return x.value}).join('')});
</script>
</body>
</html>`

const pageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="theme-color" content="#07070c">
<title>Select Files</title>
<link rel="icon" href="https://raw.githubusercontent.com/Tj-Bots/Leech-TJ/wzgram/docs/logo.png" type="image/png">
<link rel="preconnect" href="https://fonts.googleapis.com"><link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@400;500;600;700;800&display=swap" rel="stylesheet">
<style>
:root{--bg:#07070c;--glass:rgba(255,255,255,.045);--glass2:rgba(255,255,255,.075);--edge:rgba(255,255,255,.08);--tx:#f5f5fa;--dim:#8b8ba3;--a1:#7c3aed;--a2:#22d3ee;--grad:linear-gradient(135deg,#7c3aed,#4f46e5 55%,#06b6d4)}
*{box-sizing:border-box;-webkit-tap-highlight-color:transparent}
html,body{margin:0;background:var(--bg);color:var(--tx);font:15px/1.35 "Plus Jakarta Sans",system-ui,-apple-system,sans-serif;-webkit-font-smoothing:antialiased}
body{min-height:100vh;overflow-x:hidden}
.aur{position:fixed;inset:-20vh -20vw auto;height:70vh;z-index:-1;filter:blur(70px);opacity:.55;pointer-events:none}
.aur i{position:absolute;border-radius:50%;animation:fl 14s ease-in-out infinite alternate}
.aur i:nth-child(1){width:45vw;height:45vw;left:5vw;top:5vh;background:#7c3aed}
.aur i:nth-child(2){width:35vw;height:35vw;right:5vw;top:0;background:#0891b2;animation-delay:-5s}
.aur i:nth-child(3){width:30vw;height:30vw;left:35vw;top:20vh;background:#4f46e5;animation-delay:-9s}
@keyframes fl{to{transform:translate(6vw,4vh) scale(1.15)}}
svg{width:20px;height:20px;fill:none;stroke:currentColor;stroke-width:2;stroke-linecap:round;stroke-linejoin:round;flex:0 0 auto}
.wrap{max-width:720px;margin:0 auto;padding:0 14px 130px}
.bar{position:sticky;top:0;z-index:6;display:flex;align-items:center;gap:10px;padding:14px 0 10px;backdrop-filter:blur(16px);-webkit-backdrop-filter:blur(16px);background:linear-gradient(rgba(7,7,12,.85),rgba(7,7,12,.55))}
.bar img{width:36px;height:36px;border-radius:11px;box-shadow:0 4px 18px #7c3aed55}
.bar h1{flex:1;margin:0;font-size:19px;font-weight:800;letter-spacing:-.3px}
.btn-i{width:42px;height:42px;border-radius:14px;border:1px solid var(--edge);background:var(--glass);color:var(--tx);display:grid;place-items:center;cursor:pointer;text-decoration:none;transition:transform .15s,background .2s}
.btn-i:active{transform:scale(.9);background:var(--glass2)}
.btn-i.tg{color:#38bdf8}
.btn-i .dot{position:absolute;margin:-22px -22px 0 0;width:9px;height:9px;border-radius:50%;background:var(--a2);box-shadow:0 0 10px var(--a2);display:none}
.btn-i.has .dot{display:block}
.hero{display:flex;align-items:center;gap:18px;margin-top:6px;padding:18px;border-radius:24px;background:var(--glass);border:1px solid var(--edge);position:relative;overflow:hidden}
.hero:before{content:"";position:absolute;inset:0;background:radial-gradient(120% 90% at 0% 0%,#7c3aed26,transparent 60%);pointer-events:none}
.ring{position:relative;width:92px;height:92px;flex:0 0 auto}
.ring svg{width:92px;height:92px;transform:rotate(-90deg)}
.ring circle{fill:none;stroke-width:9}
.ring .bg{stroke:rgba(255,255,255,.07)}
.ring .fg{stroke:url(#g);stroke-linecap:round;stroke-dasharray:251.3;stroke-dashoffset:251.3;transition:stroke-dashoffset .6s cubic-bezier(.2,.9,.2,1)}
.ring .pc{position:absolute;inset:0;display:grid;place-items:center;font-weight:800;font-size:20px;letter-spacing:-.5px}
.hs{flex:1;min-width:0}
.hs .big{font-size:26px;font-weight:800;letter-spacing:-.6px;line-height:1.1}
.hs .big small{font-size:14px;color:var(--dim);font-weight:600;letter-spacing:0}
.hs .sub{color:var(--dim);font-size:13.5px;margin-top:6px}
.hs .sub b{color:var(--tx);font-weight:700}
.find{margin-top:12px;display:flex;align-items:center;gap:10px;padding:0 16px;border-radius:18px;background:var(--glass);border:1px solid var(--edge);color:var(--dim);transition:border-color .2s,box-shadow .2s}
.find:focus-within{border-color:#7c3aed88;box-shadow:0 0 0 4px #7c3aed22}
.find input{flex:1;min-width:0;background:none;border:0;outline:0;color:var(--tx);font:inherit;padding:13px 0}
.pill{display:none;align-items:center;gap:8px;margin-top:10px;padding:7px 8px 7px 14px;border-radius:999px;background:#7c3aed22;border:1px solid #7c3aed55;width:max-content;font-size:13px;font-weight:600;color:#c4b5fd;animation:pop .25s ease}
.pill.on{display:flex}
.pill button{border:0;background:#7c3aed55;color:#fff;width:22px;height:22px;border-radius:50%;display:grid;place-items:center;cursor:pointer;padding:0}
.pill button svg{width:13px;height:13px}
.list{margin-top:14px;border-radius:24px;background:var(--glass);border:1px solid var(--edge);overflow:hidden}
.lh{display:flex;align-items:center;gap:12px;padding:14px 16px;border-bottom:1px solid var(--edge);cursor:pointer;user-select:none}
.lh .t{flex:1;font-weight:700;font-size:14px}
.lh .t span{color:var(--dim);font-weight:500;margin-left:6px}
.item,.fhead{position:relative;display:flex;align-items:center;gap:13px;padding:11px 16px;cursor:pointer;user-select:none;overflow:hidden;transition:background .2s}
.item+.item,.fold+.item,.item+.fold,.fold+.fold{border-top:1px solid rgba(255,255,255,.04)}
.item:before{content:"";position:absolute;left:0;top:14%;bottom:14%;width:3px;border-radius:0 3px 3px 0;background:var(--grad);transform:scaleY(0);transition:transform .25s cubic-bezier(.2,.9,.2,1.3)}
.item.on{background:linear-gradient(90deg,#7c3aed1c,transparent 70%)}
.item.on:before{transform:scaleY(1)}
.ic{position:relative;width:44px;height:44px;border-radius:14px;display:grid;place-items:center;flex:0 0 auto;transition:transform .2s}
.ic svg{width:21px;height:21px}
.ic b{position:absolute;bottom:-5px;left:50%;transform:translateX(-50%);font-size:8.5px;font-weight:800;letter-spacing:.4px;padding:1px 5px;border-radius:6px;background:#11111a;border:1px solid currentColor;line-height:1.3}
.item:active .ic{transform:scale(.92)}
.k-video{background:#7c3aed24;color:#a78bfa}.k-audio{background:#ec489924;color:#f472b6}.k-image{background:#10b98124;color:#34d399}
.k-archive{background:#f59e0b24;color:#fbbf24}.k-sub{background:#06b6d424;color:#22d3ee}.k-file{background:#64748b26;color:#a1a1b5}.k-folder{background:#3b82f624;color:#60a5fa}
.tx{flex:1;min-width:0;display:flex;flex-direction:column;gap:3px}
.nm{font-size:14.5px;font-weight:600;word-break:break-word;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden}
.mt{font-size:12.5px;color:var(--dim);font-variant-numeric:tabular-nums}
.chev{color:var(--dim);transition:transform .25s}
.fold.open>.fhead .chev{transform:rotate(90deg)}
.kids{display:grid;grid-template-rows:0fr;transition:grid-template-rows .3s cubic-bezier(.2,.9,.2,1)}
.kids>*{overflow:hidden}
.fold.open>.kids{grid-template-rows:1fr}
.kids-in{padding-left:18px;background:rgba(0,0,0,.15)}
.ck{appearance:none;flex:0 0 auto;width:26px;height:26px;margin:0;border-radius:50%;border:2px solid #3d3d52;display:grid;place-items:center;cursor:pointer;transition:border-color .2s,background .2s,box-shadow .2s}
.ck:after{content:"";width:6px;height:11px;border:solid #fff;border-width:0 2.6px 2.6px 0;transform:translateY(-1px) rotate(45deg) scale(0);transition:transform .25s cubic-bezier(.3,1.6,.5,1)}
.ck:checked,.ck:indeterminate{border-color:transparent;background:var(--grad);box-shadow:0 4px 14px #7c3aed66}
.ck:checked:after{transform:translateY(-1px) rotate(45deg) scale(1)}
.ck:indeterminate:after{width:11px;height:0;border-width:0 0 2.6px 0;transform:none}
.ck.pop{animation:pop .35s cubic-bezier(.3,1.6,.5,1)}
@keyframes pop{0%{transform:scale(.7)}100%{transform:scale(1)}}
.rip{position:absolute;border-radius:50%;background:rgba(167,139,250,.25);transform:scale(0);animation:rip .55s ease-out forwards;pointer-events:none}
@keyframes rip{to{transform:scale(1);opacity:0}}
.in{animation:rise .45s cubic-bezier(.2,.9,.2,1) both;animation-delay:calc(var(--i) * 28ms)}
@keyframes rise{from{opacity:0;transform:translateY(10px)}}
.hide{display:none!important}
.none{padding:34px 16px;text-align:center;color:var(--dim)}
.dock{position:fixed;left:0;right:0;bottom:0;z-index:7;padding:14px 14px calc(14px + env(safe-area-inset-bottom));background:linear-gradient(transparent,var(--bg) 45%)}
.go{position:relative;display:flex;align-items:center;justify-content:center;gap:10px;width:100%;max-width:720px;margin:0 auto;border:0;border-radius:20px;padding:17px;font:800 16px "Plus Jakarta Sans",system-ui,sans-serif;color:#fff;background:var(--grad);box-shadow:0 12px 34px #6d28d966;cursor:pointer;overflow:hidden;transition:transform .15s,box-shadow .3s,opacity .3s}
.go:after{content:"";position:absolute;top:0;bottom:0;width:40%;left:-60%;background:linear-gradient(100deg,transparent,rgba(255,255,255,.28),transparent);animation:sh 3.6s ease-in-out infinite}
@keyframes sh{0%,55%{left:-60%}100%{left:130%}}
.go:disabled:after{display:none}
.go:active{transform:scale(.97)}
.go:disabled{background:var(--glass2);color:var(--dim);box-shadow:none;animation:none;cursor:default}
.go .n{min-width:26px;height:26px;padding:0 8px;border-radius:13px;background:rgba(255,255,255,.22);display:grid;place-items:center;font-size:13px}
.go:disabled .n{display:none}
.scrim{position:fixed;inset:0;z-index:20;background:rgba(0,0,0,.55);backdrop-filter:blur(3px);opacity:0;pointer-events:none;transition:opacity .3s}
.sheet{position:fixed;left:0;right:0;bottom:0;z-index:21;max-width:720px;margin:0 auto;padding:0 12px calc(18px + env(safe-area-inset-bottom));border-radius:28px 28px 0 0;background:#11111a;border-top:1px solid var(--edge);box-shadow:0 -20px 50px rgba(0,0,0,.6);transform:translateY(110%);transition:transform .38s cubic-bezier(.2,.9,.2,1);touch-action:none}
.sheet.drag{transition:none}
.mo .scrim{opacity:1;pointer-events:auto}.mo .sheet{transform:none}
.grip{padding:12px 0 6px;cursor:grab}
.grip i{display:block;width:42px;height:5px;margin:0 auto;border-radius:5px;background:#34344a;transition:width .2s,background .2s,height .2s}
.grip:active i,.sheet.drag .grip i{width:62px;height:6px;background:var(--grad)}
.sheet h3{margin:8px 8px 10px;font-size:12px;letter-spacing:.9px;text-transform:uppercase;color:var(--dim)}
.chips{display:grid;grid-template-columns:repeat(3,1fr);gap:8px;padding:0 4px}
.chip{position:relative;display:flex;flex-direction:column;align-items:center;gap:8px;padding:14px 6px 12px;border-radius:18px;border:1px solid var(--edge);background:var(--glass);color:var(--tx);font:600 13px "Plus Jakarta Sans",system-ui,sans-serif;cursor:pointer;overflow:hidden;transition:transform .15s,border-color .2s,background .2s}
.chip:active{transform:scale(.95)}
.chip .ic{width:40px;height:40px;border-radius:13px}
.chip small{color:var(--dim);font-weight:600;font-size:11.5px}
.chip.sel{border-color:#7c3aed;background:#7c3aed1f;box-shadow:inset 0 0 0 1px #7c3aed}
.row-b{display:flex;align-items:center;gap:12px;width:100%;margin-top:12px;padding:14px;border-radius:18px;border:1px solid var(--edge);background:var(--glass);color:var(--tx);font:600 14px "Plus Jakarta Sans",system-ui,sans-serif;cursor:pointer}
.row-b:active{background:var(--glass2)}
.toast{position:fixed;left:50%;top:16px;z-index:30;transform:translate(-50%,-180%);display:flex;align-items:center;gap:10px;max-width:92vw;width:max-content;padding:13px 18px;border-radius:18px;background:rgba(16,40,30,.92);backdrop-filter:blur(10px);border:1px solid #22c55e55;color:#bbf7d0;font-size:14px;font-weight:600;transition:transform .45s cubic-bezier(.2,1.2,.3,1)}
.toast.show{transform:translate(-50%,0)}
@media (prefers-reduced-motion:reduce){*,*:before,*:after{animation:none!important;transition:none!important}}
</style>
</head>
<body>
<div class="aur"><i></i><i></i><i></i></div>
<svg style="position:absolute;width:0;height:0;overflow:hidden" aria-hidden="true">
<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#8b5cf6"/><stop offset="1" stop-color="#22d3ee"/></linearGradient></defs>
<symbol id="i-video" viewBox="0 0 24 24"><rect x="2" y="4" width="20" height="16" rx="4"/><path d="M10 9l5 3-5 3z" fill="currentColor"/></symbol>
<symbol id="i-audio" viewBox="0 0 24 24"><path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/></symbol>
<symbol id="i-image" viewBox="0 0 24 24"><rect x="3" y="3" width="18" height="18" rx="4"/><circle cx="9" cy="9" r="2"/><path d="M21 15l-5-5L5 21"/></symbol>
<symbol id="i-archive" viewBox="0 0 24 24"><rect x="2" y="3" width="20" height="5" rx="1.5"/><path d="M4 8v11a2 2 0 002 2h12a2 2 0 002-2V8M10 12h4"/></symbol>
<symbol id="i-sub" viewBox="0 0 24 24"><rect x="2" y="5" width="20" height="14" rx="4"/><path d="M7 15h4M14 15h3M7 11h2M12 11h5"/></symbol>
<symbol id="i-file" viewBox="0 0 24 24"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z"/><path d="M14 2v6h6"/></symbol>
<symbol id="i-folder" viewBox="0 0 24 24"><path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z"/></symbol>
<symbol id="i-chev" viewBox="0 0 24 24"><path d="M9 6l6 6-6 6"/></symbol>
<symbol id="i-search" viewBox="0 0 24 24"><circle cx="11" cy="11" r="7"/><path d="M21 21l-4.3-4.3"/></symbol>
<symbol id="i-filter" viewBox="0 0 24 24"><path d="M4 6h16M7 12h10M10 18h4"/></symbol>
<symbol id="i-x" viewBox="0 0 24 24"><path d="M6 6l12 12M18 6L6 18"/></symbol>
<symbol id="i-fold" viewBox="0 0 24 24"><path d="M7 15l5 5 5-5M7 9l5-5 5 5"/></symbol>
<symbol id="i-check" viewBox="0 0 24 24"><path d="M5 12l5 5L20 7"/></symbol>
<symbol id="i-all" viewBox="0 0 24 24"><rect x="3" y="3" width="7" height="7" rx="2"/><rect x="14" y="3" width="7" height="7" rx="2"/><rect x="3" y="14" width="7" height="7" rx="2"/><rect x="14" y="14" width="7" height="7" rx="2"/></symbol>
<symbol id="i-tg" viewBox="0 0 24 24"><path d="M21.5 4.5L2.8 11.7c-.9.4-.9 1.6.1 1.9l4.6 1.4 1.8 5.5c.3.8 1.3 1 1.9.4l2.6-2.5 4.8 3.5c.7.5 1.7.1 1.9-.7l3.1-14.9c.2-1-.8-1.8-1.9-1.3z"/><path d="M8.5 14.8l9.5-7.3"/></symbol>
</svg>
<div class="toast" id="toast"><svg><use href="#i-check"/></svg><span>Saved — back to Telegram and press <b>Done Selecting</b></span></div>
<div class="wrap">
  <div class="bar">
    <img src="https://raw.githubusercontent.com/Tj-Bots/Leech-TJ/wzgram/docs/logo.png" alt="">
    <h1>Select Files</h1>
    <a class="btn-i tg" href="https://t.me/TJ_Bots" target="_blank" rel="noopener" aria-label="Our channel"><svg><use href="#i-tg"/></svg></a>
    <button type="button" class="btn-i" id="menuBtn" aria-label="Filter"><svg><use href="#i-filter"/></svg><span class="dot"></span></button>
  </div>
  <div class="hero">
    <div class="ring"><svg viewBox="0 0 92 92"><circle class="bg" cx="46" cy="46" r="40"/><circle class="fg" id="arc" cx="46" cy="46" r="40"/></svg><div class="pc" id="pc">0%</div></div>
    <div class="hs"><div class="big" id="ns">0 B <small>/ <span id="ts">0 B</span></small></div><div class="sub"><b id="nf">0</b> of <span id="tf">0</span> files selected</div></div>
  </div>
  <label class="find"><svg><use href="#i-search"/></svg><input id="q" type="search" placeholder="Search files" autocomplete="off"></label>
  <div class="pill" id="pill"><span id="pillT"></span><button type="button" id="pillX" aria-label="clear"><svg><use href="#i-x"/></svg></button></div>
  <form id="f" action="{form_url}" method="POST">
    <div class="list">
      <div class="lh" id="lh"><span class="t">All files<span id="lhc"></span></span><input type="checkbox" class="ck" id="all" aria-label="select all"></div>
      <div id="list">{My_content}</div>
      <div class="none hide" id="none">No files match</div>
    </div>
    <div class="dock"><button type="submit" class="go" id="go" disabled><svg><use href="#i-check"/></svg><span id="goT">Select files</span><span class="n" id="goN"></span></button></div>
  </form>
</div>
<div class="scrim" id="scrim"></div>
<div class="sheet" id="sheet">
  <div class="grip" id="grip"><i></i></div>
  <h3>Show</h3>
  <div class="chips">
    <button type="button" class="chip sel" data-kind=""><span class="ic k-folder"><svg><use href="#i-all"/></svg></span>All<small data-count=""></small></button>
    <button type="button" class="chip" data-kind="video"><span class="ic k-video"><svg><use href="#i-video"/></svg></span>Videos<small data-count="video"></small></button>
    <button type="button" class="chip" data-kind="audio"><span class="ic k-audio"><svg><use href="#i-audio"/></svg></span>Audio<small data-count="audio"></small></button>
    <button type="button" class="chip" data-kind="sub"><span class="ic k-sub"><svg><use href="#i-sub"/></svg></span>Subtitles<small data-count="sub"></small></button>
    <button type="button" class="chip" data-kind="image"><span class="ic k-image"><svg><use href="#i-image"/></svg></span>Images<small data-count="image"></small></button>
    <button type="button" class="chip" data-kind="archive"><span class="ic k-archive"><svg><use href="#i-archive"/></svg></span>Archives<small data-count="archive"></small></button>
  </div>
  <button type="button" class="row-b" id="foldAll"><span class="ic k-folder" style="width:36px;height:36px;border-radius:12px"><svg><use href="#i-fold"/></svg></span><span id="foldT">Collapse folders</span></button>
</div>
<script>
(function(){
  var $=function(id){return document.getElementById(id)},D=document;
  // wrap the children of every folder (for the open / close animation)
  D.querySelectorAll('.kids').forEach(function(k){var w=D.createElement('div');w.className='kids-in';while(k.firstChild)w.appendChild(k.firstChild);var o=D.createElement('div');o.appendChild(w);k.appendChild(o)});
  var files=[].slice.call(D.querySelectorAll('.fb')),folders=[].slice.call(D.querySelectorAll('.fold')),kind='',query='';
  D.querySelectorAll('.item,.fhead').forEach(function(r,i){r.style.setProperty('--i',Math.min(i,24));r.classList.add('in')});
  function fmt(b){if(!b||b<=0)return'0 B';var u=['B','KB','MB','GB','TB'],i=Math.min(4,Math.floor(Math.log(b)/Math.log(1024)));return(b/Math.pow(1024,i)).toFixed(i?2:0)+' '+u[i]}
  function buzz(){if(navigator.vibrate)navigator.vibrate(7)}
  var total=0;files.forEach(function(c){total+=+c.dataset.size||0});
  $('tf').textContent=files.length;$('ts').textContent=fmt(total);
  var counts={'':files.length};D.querySelectorAll('.item').forEach(function(r){counts[r.dataset.kind]=(counts[r.dataset.kind]||0)+1});
  D.querySelectorAll('[data-count]').forEach(function(e){var n=counts[e.dataset.count]||0;e.textContent=n+' file'+(n==1?'':'s');if(!n)e.parentNode.classList.add('hide')});
  function state(){return files.map(function(c){return c.checked?1:0}).join('')}
  var saved=state();
  function vis(){return files.filter(function(c){return !c.closest('.item').classList.contains('hide')})}
  function sync(){
    for(var i=folders.length-1;i>=0;i--){var f=folders[i],cs=[].slice.call(f.querySelectorAll('.item:not(.hide) .fb')),on=cs.filter(function(c){return c.checked}).length,b=f.querySelector('.fcb');b.checked=cs.length&&on===cs.length;b.indeterminate=on>0&&on<cs.length}
    var v=vis(),on=v.filter(function(c){return c.checked}).length,a=$('all');a.checked=v.length&&on===v.length;a.indeterminate=on>0&&on<v.length;
    $('lhc').textContent=v.length+(kind||query?' shown':'');
  }
  function update(){
    var n=0,s=0;files.forEach(function(c){c.closest('.item').classList.toggle('on',c.checked);if(c.checked){n++;s+=+c.dataset.size||0}});
    var p=total?s/total:0;$('arc').style.strokeDashoffset=251.3*(1-p);$('pc').textContent=Math.round(p*100)+'%';
    $('nf').textContent=n;$('ns').firstChild.nodeValue=fmt(s)+' ';sync();
    var same=state()===saved,g=$('go');g.disabled=n===0||same;
    $('goT').textContent=n===0?'Select at least one file':same?'Selection saved':'Confirm selection';$('goN').textContent=n;
  }
  function filter(){
    var any=false;D.querySelectorAll('.item').forEach(function(r){var m=(!query||r.querySelector('.nm').textContent.toLowerCase().indexOf(query)>=0)&&(!kind||r.dataset.kind===kind);r.classList.toggle('hide',!m);if(m)any=true});
    for(var i=folders.length-1;i>=0;i--){var f=folders[i],has=f.querySelector('.item:not(.hide)');f.classList.toggle('hide',!has);if((query||kind)&&has)f.classList.add('open')}
    $('none').classList.toggle('hide',any);
    var lbl=D.querySelector('.chip[data-kind="'+kind+'"]');$('pill').classList.toggle('on',!!kind);if(kind&&lbl)$('pillT').textContent='Showing '+lbl.childNodes[1].textContent.trim();
    $('menuBtn').classList.toggle('has',!!kind);
    D.querySelectorAll('.chip').forEach(function(c){c.classList.toggle('sel',c.dataset.kind===kind)});
    update();
  }
  function ripple(e,el){var r=el.getBoundingClientRect(),d=Math.max(r.width,r.height)*2,s=D.createElement('span');s.className='rip';s.style.width=s.style.height=d+'px';var p=e.touches?e.touches[0]:e;s.style.left=(p.clientX-r.left-d/2)+'px';s.style.top=(p.clientY-r.top-d/2)+'px';el.appendChild(s);setTimeout(function(){s.remove()},600)}
  D.addEventListener('pointerdown',function(e){var el=e.target.closest('.item,.fhead,.chip,.lh');if(el)ripple(e,el)});
  $('list').addEventListener('change',function(e){var t=e.target;t.classList.remove('pop');void t.offsetWidth;t.classList.add('pop');buzz();
    if(t.classList.contains('fcb'))t.closest('.fold').querySelectorAll('.item:not(.hide) .fb').forEach(function(c){c.checked=t.checked});update()});
  $('list').addEventListener('click',function(e){var h=e.target.closest('.fhead');if(h&&!e.target.classList.contains('ck'))h.parentNode.classList.toggle('open')});
  $('lh').addEventListener('click',function(e){if(e.target.id!=='all'){$('all').click()}});
  $('all').addEventListener('change',function(){var v=vis(),on=!v.every(function(c){return c.checked});v.forEach(function(c){c.checked=on});var a=$('all');a.classList.remove('pop');void a.offsetWidth;a.classList.add('pop');buzz();update()});
  $('q').addEventListener('input',function(){query=this.value.trim().toLowerCase();filter()});
  $('pillX').onclick=function(){kind='';filter()};
  function menu(o){var sh=$('sheet');sh.style.transform='';$('scrim').style.opacity='';D.body.classList.toggle('mo',o);if(o)buzz()}
  $('menuBtn').onclick=function(){menu(true)};$('scrim').onclick=function(){menu(false)};
  D.querySelectorAll('.chip').forEach(function(c){c.onclick=function(){kind=c.dataset.kind;filter();setTimeout(function(){menu(false)},160)}});
  $('foldAll').onclick=function(){var open=folders.some(function(f){return f.classList.contains('open')});folders.forEach(function(f){f.classList.toggle('open',!open)});$('foldT').textContent=open?'Expand folders':'Collapse folders';menu(false)};
  (function(){ // drag the sheet down: it follows the finger, the backdrop fades, a flick or half way closes
    var sh=$('sheet'),sc=$('scrim'),y0=0,dy=0,t0=0,on=false;
    function st(e){if(!D.body.classList.contains('mo'))return;var p=e.touches?e.touches[0]:e;on=true;y0=p.clientY;dy=0;t0=Date.now();sh.classList.add('drag')}
    function mv(e){if(!on)return;var p=e.touches?e.touches[0]:e,d=p.clientY-y0;dy=d>0?d:-Math.min(22,Math.sqrt(-d)*3);sh.style.transform='translateY('+dy+'px)';sc.style.opacity=Math.max(0,1-Math.max(0,dy)/sh.offsetHeight);if(e.cancelable)e.preventDefault()}
    function en(){if(!on)return;on=false;sh.classList.remove('drag');if(dy>sh.offsetHeight*.3||dy/Math.max(1,Date.now()-t0)>.6)menu(false);else{sh.style.transform='';sc.style.opacity=''}}
    [$('grip'),sh.querySelector('h3')].forEach(function(z){z.addEventListener('touchstart',st,{passive:true});z.addEventListener('mousedown',st)});
    addEventListener('touchmove',mv,{passive:false});addEventListener('mousemove',mv);addEventListener('touchend',en);addEventListener('mouseup',en);
    $('grip').addEventListener('click',function(){if(Math.abs(dy)<4)menu(false)});
  })();
  $('f').addEventListener('submit',function(e){if(!files.some(function(c){return c.checked}))e.preventDefault()});
  if(location.search.indexOf('saved=1')>=0){var t=$('toast');setTimeout(function(){t.classList.add('show')},200);setTimeout(function(){t.classList.remove('show')},5200)}
  update();
})();
</script>
</body>
</html>`
