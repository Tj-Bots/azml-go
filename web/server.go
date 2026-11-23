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
<meta name="theme-color" content="#0b0b10">
<title>Select Files</title>
<link rel="icon" href="https://raw.githubusercontent.com/Tj-Bots/Leech-TJ/wzgram/docs/logo.png" type="image/png">
<style>
:root{--bg:#0b0b10;--card:#14141c;--card2:#1b1b26;--line:#262633;--text:#f2f2f7;--dim:#9a9ab0;--acc:#a855f7;--acc2:#7c3aed;--ok:#22c55e}
*{box-sizing:border-box;-webkit-tap-highlight-color:transparent}
html,body{margin:0;background:var(--bg);color:var(--text);font:15px/1.4 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
.wrap{max-width:820px;margin:0 auto;padding:0 12px 110px}
.top{position:sticky;top:0;z-index:5;background:linear-gradient(var(--bg) 85%,transparent);padding:12px 0 10px}
.head{display:flex;align-items:center;gap:10px;margin-bottom:10px}
.head img{width:34px;height:34px;border-radius:50%;border:1px solid #a855f755}
.head h1{font-size:18px;margin:0;font-weight:700}
.stats{background:var(--card);border:1px solid var(--line);border-radius:16px;padding:12px 14px}
.srow{display:flex;justify-content:space-between;font-size:13px;color:var(--dim)}
.srow b{color:var(--text);font-size:15px}
.bar{height:6px;background:var(--card2);border-radius:9px;margin-top:9px;overflow:hidden}
.bar i{display:block;height:100%;width:0;background:linear-gradient(90deg,var(--acc2),var(--acc));transition:width .2s}
.tools{display:flex;gap:8px;margin-top:10px;overflow-x:auto;scrollbar-width:none}
.tools::-webkit-scrollbar{display:none}
.search{flex:1 0 160px;background:var(--card);border:1px solid var(--line);color:var(--text);border-radius:12px;padding:9px 12px;font-size:14px;outline:none}
.search:focus{border-color:var(--acc)}
.chip{flex:0 0 auto;background:var(--card);border:1px solid var(--line);color:var(--text);border-radius:12px;padding:8px 12px;font-size:13px;cursor:pointer}
.chip:active{background:var(--card2)}
.list{background:var(--card);border:1px solid var(--line);border-radius:16px;padding:6px}
.row{display:flex;align-items:center;gap:10px;padding:10px 8px;border-radius:10px;cursor:pointer;user-select:none;min-height:44px}
.row:active{background:var(--card2)}
.name{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:14px}
.frow .name{font-weight:600}
.meta{flex:0 0 auto;color:var(--dim);font-size:12px;font-variant-numeric:tabular-nums}
.ico{flex:0 0 auto;width:22px;text-align:center}
.chev{flex:0 0 auto;width:22px;height:22px;border:0;background:none;color:var(--dim);font-size:14px;cursor:pointer;transition:transform .15s;padding:0}
.folder.open>.frow .chev{transform:rotate(90deg)}
.kids{margin-left:14px;padding-left:8px;border-left:1px solid var(--line);display:none}
.folder.open>.kids{display:block}
.hide{display:none!important}
.cb{appearance:none;flex:0 0 auto;width:22px;height:22px;border:2px solid #4a4a5e;border-radius:7px;display:grid;place-content:center;margin:0;cursor:pointer;transition:.12s}
.cb::after{content:"";width:11px;height:6px;border:solid #fff;border-width:0 0 2.5px 2.5px;transform:rotate(-45deg) scale(0);margin-top:-3px;transition:.12s}
.cb:checked,.cb:indeterminate{background:var(--acc);border-color:var(--acc)}
.cb:checked::after{transform:rotate(-45deg) scale(1)}
.cb:indeterminate::after{width:10px;height:0;border-width:0 0 2.5px 0;transform:none;margin:0}
.file.on .name{color:#fff}
.bottom{position:fixed;left:0;right:0;bottom:0;padding:12px 12px calc(12px + env(safe-area-inset-bottom));background:linear-gradient(transparent,var(--bg) 35%)}
.confirm{display:block;width:100%;max-width:820px;margin:0 auto;border:0;border-radius:14px;padding:15px;font-size:16px;font-weight:700;color:#fff;background:linear-gradient(90deg,var(--acc2),var(--acc));box-shadow:0 8px 24px #a855f740;cursor:pointer}
.confirm:disabled{background:var(--card2);color:var(--dim);box-shadow:none;cursor:default}
.toast{position:fixed;left:50%;top:14px;transform:translate(-50%,-150%);background:#123d24;color:#c7f9d6;border:1px solid #22c55e66;padding:11px 16px;border-radius:12px;font-size:14px;z-index:9;transition:transform .3s;max-width:92vw;text-align:center}
.toast.show{transform:translate(-50%,0)}
.empty{color:var(--dim);text-align:center;padding:24px;font-size:14px}
</style>
</head>
<body>
<div class="toast" id="toast">✓ Saved! Go back to Telegram and press <b>Done Selecting</b></div>
<div class="wrap">
  <div class="top">
    <div class="head"><img src="https://raw.githubusercontent.com/Tj-Bots/Leech-TJ/wzgram/docs/logo.png" alt=""><h1>Select Files</h1></div>
    <div class="stats">
      <div class="srow"><span>Files <b id="nf">0</b> / <span id="tf">0</span></span><span>Size <b id="ns">0 B</b> / <span id="ts">0 B</span></span></div>
      <div class="bar"><i id="bar"></i></div>
    </div>
    <div class="tools">
      <input class="search" id="q" type="search" placeholder="🔍 Search files" autocomplete="off">
      <button type="button" class="chip" data-act="all">All</button>
      <button type="button" class="chip" data-act="none">None</button>
      <button type="button" class="chip" data-act="video">🎬 Videos</button>
      <button type="button" class="chip" data-act="invert">Invert</button>
      <button type="button" class="chip" data-act="fold">Collapse</button>
    </div>
  </div>
  <form id="f" action="{form_url}" method="POST">
    <div class="list" id="list">{My_content}<div class="empty hide" id="empty">No files match your search</div></div>
    <div class="bottom"><button type="submit" class="confirm" id="go" disabled>Confirm Selection</button></div>
  </form>
</div>
<script>
(function(){
  var files=[].slice.call(document.querySelectorAll('.fileb'));
  var folders=[].slice.call(document.querySelectorAll('.folder'));
  function fmt(b){if(!b||b<=0)return'0 B';var u=['B','KB','MB','GB','TB'],i=Math.min(4,Math.floor(Math.log(b)/Math.log(1024)));return(b/Math.pow(1024,i)).toFixed(i?2:0)+' '+u[i]}
  var total=0;files.forEach(function(c){total+=+c.dataset.size||0});
  document.getElementById('tf').textContent=files.length;document.getElementById('ts').textContent=fmt(total);
  function state(){return files.map(function(c){return c.checked?1:0}).join('')}
  var saved=state();
  function folderSync(){
    for(var i=folders.length-1;i>=0;i--){
      var fo=folders[i],cs=fo.querySelectorAll('.fileb'),on=0;
      for(var j=0;j<cs.length;j++)if(cs[j].checked)on++;
      var fcb=fo.querySelector('.fcb');fcb.checked=cs.length>0&&on===cs.length;fcb.indeterminate=on>0&&on<cs.length;
    }
  }
  function update(){
    var n=0,s=0;files.forEach(function(c){c.closest('.file').classList.toggle('on',c.checked);if(c.checked){n++;s+=+c.dataset.size||0}});
    document.getElementById('nf').textContent=n;document.getElementById('ns').textContent=fmt(s);
    document.getElementById('bar').style.width=(total?s/total*100:0)+'%';
    folderSync();
    var go=document.getElementById('go');
    go.disabled=n===0||state()===saved;
    go.textContent=n===0?'Select at least one file':(state()===saved?'Saved ✓':'Confirm '+n+' file'+(n>1?'s':'')+' · '+fmt(s));
  }
  document.getElementById('list').addEventListener('change',function(e){
    var t=e.target;if(t.classList.contains('fcb')){t.closest('.folder').querySelectorAll('.fileb').forEach(function(c){if(!c.closest('.file').classList.contains('hide'))c.checked=t.checked})}
    update();
  });
  document.getElementById('list').addEventListener('click',function(e){
    var row=e.target.closest('.frow');if(!row||e.target.classList.contains('cb'))return;
    row.parentNode.classList.toggle('open');
  });
  function visible(){return files.filter(function(c){return !c.closest('.file').classList.contains('hide')})}
  document.querySelectorAll('.chip').forEach(function(b){b.addEventListener('click',function(){
    var a=b.dataset.act;
    if(a==='fold'){var open=folders.some(function(f){return f.classList.contains('open')});folders.forEach(function(f){f.classList.toggle('open',!open)});b.textContent=open?'Expand':'Collapse';return}
    visible().forEach(function(c){
      if(a==='all')c.checked=true;else if(a==='none')c.checked=false;else if(a==='invert')c.checked=!c.checked;
      else if(a==='video')c.checked=c.closest('.file').dataset.kind==='video';
    });update();
  })});
  document.getElementById('q').addEventListener('input',function(){
    var q=this.value.trim().toLowerCase(),any=false;
    document.querySelectorAll('.file').forEach(function(r){var m=!q||r.querySelector('.name').textContent.toLowerCase().indexOf(q)>=0;r.classList.toggle('hide',!m);if(m)any=true});
    for(var i=folders.length-1;i>=0;i--){var fo=folders[i],has=fo.querySelector('.file:not(.hide)');fo.classList.toggle('hide',!has);if(q&&has)fo.classList.add('open')}
    document.getElementById('empty').classList.toggle('hide',any);
  });
  document.getElementById('f').addEventListener('submit',function(e){if(!files.some(function(c){return c.checked})){e.preventDefault()}});
  if(location.search.indexOf('saved=1')>=0){var t=document.getElementById('toast');t.classList.add('show');setTimeout(function(){t.classList.remove('show')},4500)}
  update();
})();
</script>
</body>
</html>`
