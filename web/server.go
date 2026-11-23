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
<html class="dark" lang="en">
<head>
    <style>*{-webkit-tap-highlight-color:transparent}</style>
    <meta charset="utf-8"/>
    <meta content="width=device-width, initial-scale=1.0" name="viewport"/>
    <title>Security Access - File Selector</title>
    <script src="https://cdn.tailwindcss.com?plugins=forms,container-queries"></script>
    <link href="https://fonts.googleapis.com/css2?family=Manrope:wght@400;700;800&family=Inter:wght@400;500;600&display=swap" rel="stylesheet"/>
    <link href="https://fonts.googleapis.com/css2?family=Material+Symbols+Outlined:wght,FILL@100..700,0..1&display=swap" rel="stylesheet"/>
    <style>
        body { background-color: #000000; color: #fafafa; }
        .glass-card {
            background: rgba(10, 10, 12, 0.8);
            backdrop-filter: blur(20px);
            border: 1px solid #27272a;
        }
    </style>
</head>
<body class="antialiased min-h-screen flex items-center justify-center p-6 relative font-sans">
    <div class="w-full max-w-lg glass-card rounded-2xl shadow-2xl overflow-hidden text-center p-10 relative">
        <div class="w-16 h-16 mx-auto rounded-full bg-zinc-900 border border-zinc-700 flex items-center justify-center mb-6">
            <span class="material-symbols-outlined text-purple-500 text-3xl">lock</span>
        </div>
        <h1 class="text-2xl font-extrabold text-white mb-2">Authorization Required</h1>
        <p class="text-zinc-400 mb-8 text-sm">Enter the 4-digit security PIN provided in Telegram to access your files.</p>
        <form action="{form_url}">
            <div class="mb-8">
                <input 
                    type="text" 
                    name="pin_code"
                    placeholder="Enter 4-digit PIN"
                    class="w-full bg-black border border-zinc-700 rounded-xl px-6 py-4 text-center text-xl tracking-[0.5em] text-white focus:border-purple-500 focus:outline-none transition-all font-mono"
                    autocomplete="off"
                    maxlength="4"
                    required
                />
            </div>
            <button type="submit" class="w-full bg-purple-600 hover:bg-purple-700 text-white font-bold py-4 rounded-xl transition-all shadow-[0_0_20px_rgba(168,85,247,0.3)]">
                Authenticate
            </button>
        </form>
    </div>
</body>
</html>`

const pageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="theme-color" content="#0a0a0f">
<title>Select Files</title>
<link rel="icon" href="https://raw.githubusercontent.com/Tj-Bots/Leech-TJ/wzgram/docs/logo.png" type="image/png">
<link rel="preconnect" href="https://fonts.googleapis.com"><link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
<style>
:root{--bg:#0a0a0f;--s1:#13131b;--s2:#1a1a25;--s3:#232332;--line:#262636;--tx:#f4f4f8;--dim:#8d8da6;--acc:#8b5cf6;--acc2:#a78bfa}
*{box-sizing:border-box;-webkit-tap-highlight-color:transparent}
html,body{margin:0;background:var(--bg);color:var(--tx);font:15px/1.35 Inter,system-ui,-apple-system,sans-serif}
body{background:radial-gradient(1200px 500px at 50% -200px,#8b5cf622,transparent),var(--bg)}
svg{width:20px;height:20px;fill:none;stroke:currentColor;stroke-width:2;stroke-linecap:round;stroke-linejoin:round;flex:0 0 auto}
.wrap{max-width:760px;margin:0 auto;padding:0 14px 120px}
.top{position:sticky;top:0;z-index:5;padding:14px 0 12px;background:linear-gradient(var(--bg) 80%,#0a0a0f00)}
.head{display:flex;align-items:center;gap:12px}
.head img{width:38px;height:38px;border-radius:12px}
.head .t{flex:1;min-width:0}
.head h1{font-size:18px;margin:0;font-weight:700;letter-spacing:-.2px}
.head p{margin:2px 0 0;color:var(--dim);font-size:12.5px}
.ibtn{width:42px;height:42px;border-radius:12px;border:1px solid var(--line);background:var(--s1);color:var(--tx);display:grid;place-items:center;cursor:pointer}
.ibtn:active{background:var(--s2)}
.card{margin-top:12px;background:var(--s1);border:1px solid var(--line);border-radius:18px;padding:14px 16px}
.kpis{display:flex;gap:12px}
.kpi{flex:1;white-space:nowrap}
.kpi>span{display:block;color:var(--dim);font-size:11.5px;text-transform:uppercase;letter-spacing:.6px;font-weight:600}
.kpi b{font-size:20px;font-weight:700}
.kpi small{color:var(--dim);font-size:13px;font-weight:500}
.bar{height:6px;background:var(--s3);border-radius:9px;margin-top:12px;overflow:hidden}
.bar i{display:block;height:100%;width:0;border-radius:9px;background:linear-gradient(90deg,#6d28d9,var(--acc2));transition:width .25s}
.srch-row{display:flex;gap:8px;margin-top:10px}
.srch-row .search{margin-top:0;flex:1;min-width:0}
.search input{min-width:0;width:100%}
.selall{flex:0 0 auto;display:flex;align-items:center;gap:6px;border:1px solid var(--line);background:var(--s1);color:var(--tx);border-radius:14px;padding:0 14px;font:600 14px Inter,system-ui,sans-serif;cursor:pointer;transition:.15s}
.selall:active{transform:scale(.96)}
.selall.on{background:var(--acc);border-color:var(--acc);color:#fff}
.ibtn.tg{color:#38bdf8;text-decoration:none;margin-right:2px}
.search{margin-top:10px;display:flex;align-items:center;gap:10px;background:var(--s1);border:1px solid var(--line);border-radius:14px;padding:0 14px;color:var(--dim)}
.search:focus-within{border-color:var(--acc)}
.search input{flex:1;background:none;border:0;outline:none;color:var(--tx);font:inherit;padding:12px 0}
.fchip{display:none;margin-top:10px;align-items:center;gap:8px;font-size:13px;color:var(--acc2)}
.fchip.on{display:flex}
.fchip button{background:var(--s2);border:1px solid var(--line);color:var(--tx);border-radius:9px;padding:4px 10px;font:inherit;font-size:12px;cursor:pointer}
.list{background:var(--s1);border:1px solid var(--line);border-radius:18px;padding:6px;margin-top:4px}
.row{display:flex;align-items:center;gap:12px;padding:10px;border-radius:12px;cursor:pointer;user-select:none;transition:background .12s}
.row:active{background:var(--s2)}
.file.on{background:#8b5cf614}
.tile{width:40px;height:40px;border-radius:11px;display:grid;place-items:center;flex:0 0 auto}
.tile svg{width:20px;height:20px}
.t-video{background:#8b5cf622;color:#a78bfa}.t-audio{background:#ec489922;color:#f472b6}.t-image{background:#22c55e22;color:#4ade80}
.t-archive{background:#f59e0b22;color:#fbbf24}.t-sub{background:#06b6d422;color:#22d3ee}.t-file{background:#64748b22;color:#94a3b8}.t-folder{background:#3b82f622;color:#60a5fa}
.txt{flex:1;min-width:0;display:flex;flex-direction:column;gap:2px}
.name{font-size:14px;font-weight:500;word-break:break-word;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden}
.frow .name{font-weight:600}
.sub{font-size:12px;color:var(--dim)}
.sub b{font-weight:600;color:#b9b9cf;font-size:11px;letter-spacing:.3px}
.chev{color:var(--dim);transition:transform .2s}
.folder.open>.frow .chev{transform:rotate(90deg)}
.kids{margin-left:20px;padding-left:6px;border-left:1px solid var(--line);display:none}
.folder.open>.kids{display:block}
.hide{display:none!important}
.cb{appearance:none;flex:0 0 auto;width:24px;height:24px;border:2px solid #44445a;border-radius:8px;display:grid;place-content:center;margin:0;cursor:pointer;transition:.15s;background:transparent}
.cb::after{content:"";width:12px;height:7px;border:solid #fff;border-width:0 0 2.5px 2.5px;transform:rotate(-45deg) scale(0);margin-top:-3px;transition:transform .15s}
.cb:checked,.cb:indeterminate{background:var(--acc);border-color:var(--acc)}
.cb:checked::after{transform:rotate(-45deg) scale(1)}
.cb:indeterminate::after{width:10px;height:0;border-width:0 0 2.5px 0;transform:none;margin:0}
.empty{color:var(--dim);text-align:center;padding:28px 10px;font-size:14px}
.bottom{position:fixed;left:0;right:0;bottom:0;padding:14px 14px calc(14px + env(safe-area-inset-bottom));background:linear-gradient(#0a0a0f00,var(--bg) 40%)}
.confirm{display:flex;align-items:center;justify-content:center;gap:10px;width:100%;max-width:760px;margin:0 auto;border:0;border-radius:16px;padding:16px;font:600 16px Inter,system-ui,sans-serif;color:#fff;background:linear-gradient(90deg,#6d28d9,var(--acc));box-shadow:0 10px 30px #8b5cf640;cursor:pointer}
.confirm:disabled{background:var(--s2);color:var(--dim);box-shadow:none;cursor:default}
.scrim{position:fixed;inset:0;background:#0009;opacity:0;pointer-events:none;transition:opacity .2s;z-index:20}
.sheet{position:fixed;left:0;right:0;bottom:0;z-index:21;background:var(--s1);border-top:1px solid var(--line);border-radius:24px 24px 0 0;padding:0 10px calc(16px + env(safe-area-inset-bottom));transform:translateY(105%);transition:transform .32s cubic-bezier(.2,.9,.3,1);max-width:760px;margin:0 auto;box-shadow:0 -10px 40px #0008;touch-action:none}
.sheet.drag{transition:none}
.grabzone{padding:10px 0 8px;cursor:grab;touch-action:none}
.menu-open .scrim{opacity:1;pointer-events:auto}.menu-open .sheet{transform:none}
.grab{width:40px;height:5px;border-radius:5px;background:#3a3a4e;margin:0 auto;transition:width .2s,height .2s,background .2s}
.grabzone:active .grab,.sheet.drag .grab{width:56px;height:6px;background:var(--acc)}
.scrim{transition:opacity .25s}
.sheet h3{margin:4px 10px 8px;font-size:12px;color:var(--dim);text-transform:uppercase;letter-spacing:.7px}
.mi{display:flex;align-items:center;gap:14px;width:100%;border:0;background:none;color:var(--tx);font:500 15px Inter,system-ui,sans-serif;padding:12px 10px;border-radius:12px;cursor:pointer;text-align:left}
.mi:active{background:var(--s2)}
.mi .tile{width:36px;height:36px;border-radius:10px}
.mi small{margin-left:auto;color:var(--dim);font-size:13px}
.sep{height:1px;background:var(--line);margin:6px 10px}
.toast{position:fixed;left:50%;top:14px;transform:translate(-50%,-160%);display:flex;align-items:center;gap:10px;background:#0f2a1c;color:#bbf7d0;border:1px solid #22c55e55;padding:12px 16px;border-radius:14px;font-size:14px;z-index:30;transition:transform .3s;width:max-content;max-width:92vw}
.toast.show{transform:translate(-50%,0)}
</style>
</head>
<body>
<svg style="display:none">
<symbol id="i-video" viewBox="0 0 24 24"><rect x="2" y="4" width="20" height="16" rx="3"/><path d="M10 9l5 3-5 3z" fill="currentColor"/></symbol>
<symbol id="i-audio" viewBox="0 0 24 24"><path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/></symbol>
<symbol id="i-image" viewBox="0 0 24 24"><rect x="3" y="3" width="18" height="18" rx="3"/><circle cx="9" cy="9" r="2"/><path d="M21 15l-5-5L5 21"/></symbol>
<symbol id="i-archive" viewBox="0 0 24 24"><rect x="2" y="3" width="20" height="5" rx="1.5"/><path d="M4 8v11a2 2 0 002 2h12a2 2 0 002-2V8M10 12h4"/></symbol>
<symbol id="i-sub" viewBox="0 0 24 24"><rect x="2" y="5" width="20" height="14" rx="3"/><path d="M7 15h4M14 15h3M7 11h2M12 11h5"/></symbol>
<symbol id="i-file" viewBox="0 0 24 24"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z"/><path d="M14 2v6h6"/></symbol>
<symbol id="i-folder" viewBox="0 0 24 24"><path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z"/></symbol>
<symbol id="i-chev" viewBox="0 0 24 24"><path d="M9 6l6 6-6 6"/></symbol>
<symbol id="i-search" viewBox="0 0 24 24"><circle cx="11" cy="11" r="7"/><path d="M21 21l-4.3-4.3"/></symbol>
<symbol id="i-menu" viewBox="0 0 24 24"><path d="M4 6h16M4 12h16M4 18h16"/></symbol>
<symbol id="i-all" viewBox="0 0 24 24"><rect x="3" y="3" width="18" height="18" rx="4"/><path d="M8 12l3 3 5-6"/></symbol>
<symbol id="i-none" viewBox="0 0 24 24"><rect x="3" y="3" width="18" height="18" rx="4"/></symbol>
<symbol id="i-invert" viewBox="0 0 24 24"><path d="M7 4L3 8l4 4M3 8h14M17 20l4-4-4-4M21 16H7"/></symbol>
<symbol id="i-fold" viewBox="0 0 24 24"><path d="M7 15l5 5 5-5M7 9l5-5 5 5"/></symbol>
<symbol id="i-check" viewBox="0 0 24 24"><path d="M5 12l5 5L20 7"/></symbol>
<symbol id="i-tg" viewBox="0 0 24 24"><path d="M21.5 4.5L2.8 11.7c-.9.4-.9 1.6.1 1.9l4.6 1.4 1.8 5.5c.3.8 1.3 1 1.9.4l2.6-2.5 4.8 3.5c.7.5 1.7.1 1.9-.7l3.1-14.9c.2-1-.8-1.8-1.9-1.3z"/><path d="M8.5 14.8l9.5-7.3"/></symbol>
</svg>
<div class="toast" id="toast"><svg><use href="#i-check"/></svg><span>Saved — go back to Telegram and press <b>Done Selecting</b></span></div>
<div class="wrap">
  <div class="top">
    <div class="head">
      <img src="https://raw.githubusercontent.com/Tj-Bots/Leech-TJ/wzgram/docs/logo.png" alt="">
      <div class="t"><h1>Select Files</h1><p id="hint">Choose what to download</p></div>
      <a class="ibtn tg" href="https://t.me/TJ_Bots" target="_blank" rel="noopener" aria-label="Our channel"><svg><use href="#i-tg"/></svg></a>
      <button type="button" class="ibtn" id="menuBtn" aria-label="Filter"><svg><use href="#i-menu"/></svg></button>
    </div>
    <div class="card">
      <div class="kpis">
        <div class="kpi"><span>Files</span><b id="nf">0</b> <small>/ <span id="tf">0</span></small></div>
        <div class="kpi" style="text-align:right"><span>Size</span><b id="ns">0 B</b> <small>/ <span id="ts">0 B</span></small></div>
      </div>
      <div class="bar"><i id="bar"></i></div>
    </div>
    <div class="srch-row"><label class="search"><svg><use href="#i-search"/></svg><input id="q" type="search" placeholder="Search files" autocomplete="off"></label><button type="button" class="selall" id="selAll"><svg><use href="#i-all"/></svg><span>All</span></button></div>
    <div class="fchip" id="fchip"><span id="fname"></span><button type="button" id="fclear">Show all</button></div>
  </div>
  <form id="f" action="{form_url}" method="POST">
    <div class="list" id="list">{My_content}<div class="empty hide" id="empty">No files match</div></div>
    <div class="bottom"><button type="submit" class="confirm" id="go" disabled><svg><use href="#i-check"/></svg><span id="goT">Select files</span></button></div>
  </form>
</div>
<div class="scrim" id="scrim"></div>
<div class="sheet" id="sheet">
  <div class="grabzone" id="grab"><div class="grab"></div></div>
  <h3>Show only</h3>
  <button type="button" class="mi" data-kind=""><span class="tile t-folder"><svg><use href="#i-file"/></svg></span>All files<small id="allCount"></small></button>
  <button type="button" class="mi" data-kind="video"><span class="tile t-video"><svg><use href="#i-video"/></svg></span>Videos<small data-count="video"></small></button>
  <button type="button" class="mi" data-kind="audio"><span class="tile t-audio"><svg><use href="#i-audio"/></svg></span>Audio<small data-count="audio"></small></button>
  <button type="button" class="mi" data-kind="sub"><span class="tile t-sub"><svg><use href="#i-sub"/></svg></span>Subtitles<small data-count="sub"></small></button>
  <button type="button" class="mi" data-kind="image"><span class="tile t-image"><svg><use href="#i-image"/></svg></span>Images<small data-count="image"></small></button>
  <button type="button" class="mi" data-kind="archive"><span class="tile t-archive"><svg><use href="#i-archive"/></svg></span>Archives<small data-count="archive"></small></button>
  <div class="sep"></div>
  <button type="button" class="mi" data-act="fold"><span class="tile t-folder"><svg><use href="#i-fold"/></svg></span><span id="foldT">Collapse folders</span></button>
</div>
<script>
(function(){
  var $=function(id){return document.getElementById(id)};
  var files=[].slice.call(document.querySelectorAll('.fileb'));
  var folders=[].slice.call(document.querySelectorAll('.folder'));
  var kindF='',query='';
  function fmt(b){if(!b||b<=0)return'0 B';var u=['B','KB','MB','GB','TB'],i=Math.min(4,Math.floor(Math.log(b)/Math.log(1024)));return(b/Math.pow(1024,i)).toFixed(i?2:0)+' '+u[i]}
  var total=0;files.forEach(function(c){total+=+c.dataset.size||0});
  $('tf').textContent=files.length;$('ts').textContent=fmt(total);
  var counts={};document.querySelectorAll('.file').forEach(function(r){counts[r.dataset.kind]=(counts[r.dataset.kind]||0)+1});
  document.querySelectorAll('[data-count]').forEach(function(e){var n=counts[e.dataset.count]||0;e.textContent=n;if(!n)e.closest('.mi').classList.add('hide')});
  function state(){return files.map(function(c){return c.checked?1:0}).join('')}
  var saved=state();
  function sync(){
    for(var i=folders.length-1;i>=0;i--){var fo=folders[i],cs=fo.querySelectorAll('.fileb'),on=0;for(var j=0;j<cs.length;j++)if(cs[j].checked)on++;var f=fo.querySelector('.fcb');f.checked=cs.length>0&&on===cs.length;f.indeterminate=on>0&&on<cs.length}
  }
  function update(){
    var n=0,s=0;files.forEach(function(c){c.closest('.file').classList.toggle('on',c.checked);if(c.checked){n++;s+=+c.dataset.size||0}});
    $('nf').textContent=n;$('ns').textContent=fmt(s);$('bar').style.width=(total?s/total*100:0)+'%';sync();
    var v=vis(),allOn=v.length>0&&v.every(function(c){return c.checked});
    $('selAll').classList.toggle('on',allOn);$('selAll').querySelector('span').textContent=allOn?'None':'All';
    $('selAll').querySelector('use').setAttribute('href',allOn?'#i-none':'#i-all');
    var same=state()===saved,go=$('go');go.disabled=n===0||same;
    $('goT').textContent=n===0?'Select at least one file':(same?'Selection saved':'Confirm '+n+' file'+(n>1?'s':'')+' · '+fmt(s));
  }
  function filter(){
    var any=false;
    document.querySelectorAll('.file').forEach(function(r){var m=(!query||r.querySelector('.name').textContent.toLowerCase().indexOf(query)>=0)&&(!kindF||r.dataset.kind===kindF);r.classList.toggle('hide',!m);if(m)any=true});
    for(var i=folders.length-1;i>=0;i--){var fo=folders[i],has=fo.querySelector('.file:not(.hide)');fo.classList.toggle('hide',!has);if((query||kindF)&&has)fo.classList.add('open')}
    $('empty').classList.toggle('hide',any);
    $('fchip').classList.toggle('on',!!kindF);
  }
  function vis(){return files.filter(function(c){return !c.closest('.file').classList.contains('hide')})}
  $('list').addEventListener('change',function(e){var t=e.target;if(t.classList.contains('fcb')){t.closest('.folder').querySelectorAll('.file:not(.hide) .fileb').forEach(function(c){c.checked=t.checked})}update()});
  $('list').addEventListener('click',function(e){var r=e.target.closest('.frow');if(r&&!e.target.classList.contains('cb'))r.parentNode.classList.toggle('open')});
  function menu(o){var sh=$('sheet');sh.style.transform='';$('scrim').style.opacity='';document.body.classList.toggle('menu-open',o);if(o&&navigator.vibrate)navigator.vibrate(8)}
  (function(){ // drag the sheet down with the finger: it follows, the backdrop fades, a flick or half way closes it
    var sh=$('sheet'),sc=$('scrim'),y0=0,dy=0,t0=0,on=false;
    function start(e){if(!document.body.classList.contains('menu-open'))return;var p=e.touches?e.touches[0]:e;on=true;y0=p.clientY;dy=0;t0=Date.now();sh.classList.add('drag')}
    function move(e){if(!on)return;var p=e.touches?e.touches[0]:e;dy=Math.max(0,p.clientY-y0);if(p.clientY-y0<0)dy=-Math.min(24,Math.sqrt(y0-p.clientY)*3);sh.style.transform='translateY('+dy+'px)';sc.style.opacity=Math.max(0,1-Math.max(0,dy)/sh.offsetHeight);if(e.cancelable)e.preventDefault()}
    function end(){if(!on)return;on=false;sh.classList.remove('drag');var v=dy/Math.max(1,Date.now()-t0);if(dy>sh.offsetHeight*0.35||v>0.6){menu(false)}else{sh.style.transform='';sc.style.opacity=''}}
    [$('grab'),sh.querySelector('h3')].forEach(function(z){z.addEventListener('touchstart',start,{passive:true});z.addEventListener('mousedown',start)});
    sh.addEventListener('touchstart',function(e){if(sh.scrollTop<=0&&!e.target.closest('.mi'))start(e)},{passive:true});
    window.addEventListener('touchmove',move,{passive:false});window.addEventListener('mousemove',move);
    window.addEventListener('touchend',end);window.addEventListener('mouseup',end);
    $('grab').addEventListener('click',function(){if(Math.abs(dy)<4)menu(false)});
  })();
  $('menuBtn').onclick=function(){menu(true)};$('scrim').onclick=function(){menu(false)};
  document.querySelectorAll('.mi').forEach(function(b){b.onclick=function(){
    var a=b.dataset.act,k=b.dataset.kind;
    if(k!==undefined&&a===undefined){kindF=k;$('fname').textContent='Showing: '+b.textContent.replace(/\d+$/,'').trim();filter();update()}
    else if(a==='fold'){var open=folders.some(function(f){return f.classList.contains('open')});folders.forEach(function(f){f.classList.toggle('open',!open)});$('foldT').textContent=open?'Expand folders':'Collapse folders'}

    menu(false);
  }});
  $('fclear').onclick=function(){kindF='';filter();update()};
  $('allCount').textContent=files.length;
  // one button: All when something is not selected, None when everything (shown) is
  $('selAll').onclick=function(){var v=vis(),all=v.length>0&&v.every(function(c){return c.checked});v.forEach(function(c){c.checked=!all});update();if(navigator.vibrate)navigator.vibrate(6)};
  $('q').addEventListener('input',function(){query=this.value.trim().toLowerCase();filter()});
  $('f').addEventListener('submit',function(e){if(!files.some(function(c){return c.checked}))e.preventDefault()});
  if(location.search.indexOf('saved=1')>=0){var t=$('toast');t.classList.add('show');setTimeout(function(){t.classList.remove('show')},5000)}
  update();
})();
</script>
</body>
</html>`
