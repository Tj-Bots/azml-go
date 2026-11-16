package ext

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"azml-go/config"
)

// Debrid services like the Python bot: DEBRID_LINK_API (debrid-link.com) and REAL_DEBRID_API
// (real-debrid.com) turn a link of a supported host (Mega, 1fichier, Rapidgator ...) into a fast
// direct link from their servers, without the host's limits.

var debridHTTP = &http.Client{Timeout: 60 * time.Second}

func hostIn(link string, sites []string) bool {
	u, err := url.Parse(link)
	if err != nil || u.Hostname() == "" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	for _, s := range sites {
		if strings.Contains(h, s) {
			return true
		}
	}
	return false
}

// DebridLink returns a direct link for link, or "" when no debrid service handles it.
func DebridLink(ctx context.Context, link string) (string, error) {
	cfg := config.Get()
	if cfg.DebridLinkAPI != "" && hostIn(link, debridLinkSites) {
		if d, err := debridLinkUnlock(ctx, cfg.DebridLinkAPI, link); err == nil {
			return d, nil
		} else {
			log.Printf("[Debrid] debrid-link: %v", err)
			if cfg.RealDebridAPI == "" {
				return "", err
			}
		}
	}
	if cfg.RealDebridAPI != "" && hostIn(link, realDebridSites) {
		return realDebridUnlock(ctx, cfg.RealDebridAPI, link)
	}
	return "", nil
}

func debridLinkUnlock(ctx context.Context, token, link string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://debrid-link.com/api/v2/downloader/add?access_token="+url.QueryEscape(token),
		strings.NewReader(url.Values{"url": {link}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := debridHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		Success bool            `json:"success"`
		Error   string          `json:"error"`
		Value   json.RawMessage `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	if !r.Success {
		return "", fmt.Errorf("debrid-link: %s", r.Error)
	}
	var one struct {
		DownloadURL string `json:"downloadUrl"`
	}
	if json.Unmarshal(r.Value, &one) == nil && one.DownloadURL != "" {
		return one.DownloadURL, nil
	}
	var many []struct {
		DownloadURL string `json:"downloadUrl"`
		Expired     bool   `json:"expired"`
	}
	if json.Unmarshal(r.Value, &many) == nil {
		for _, x := range many {
			if !x.Expired && x.DownloadURL != "" {
				return x.DownloadURL, nil // folders: the first file
			}
		}
	}
	return "", fmt.Errorf("debrid-link: no download url")
}

func realDebridUnlock(ctx context.Context, token, link string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.real-debrid.com/rest/1.0/unrestrict/link?auth_token="+url.QueryEscape(token),
		strings.NewReader(url.Values{"link": {link}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := debridHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		Download string `json:"download"`
		Error    string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	if r.Download == "" {
		return "", fmt.Errorf("real-debrid: %s", r.Error)
	}
	return r.Download, nil
}

var debridLinkSites = []string{
	"1dl.net", "1fichier.com", "alterupload.com", "cjoint.net", "desfichiers.com", "dfichiers.com",
	"megadl.org", "megadl.fr", "mesfichiers.fr", "mesfichiers.org", "piecejointe.net", "pjointe.com",
	"tenvoi.com", "dl4free.com", "apkadmin.com", "bayfiles.com", "clicknupload.link", "clicknupload.org",
	"clicknupload.co", "clicknupload.cc", "clicknupload.link", "clicknupload.download", "clicknupload.club",
	"clickndownload.org", "ddl.to", "ddownload.com", "depositfiles.com", "dfile.eu", "dropapk.to",
	"drop.download", "dropbox.com", "easybytez.com", "easybytez.eu", "easybytez.me", "elitefile.net",
	"elfile.net", "wdupload.com", "emload.com", "fastfile.cc", "fembed.com", "feurl.com", "anime789.com",
	"24hd.club", "vcdn.io", "sharinglink.club", "votrefiles.club", "there.to", "femoload.xyz", "dailyplanet.pw",
	"jplayer.net", "xstreamcdn.com", "gcloud.live", "vcdnplay.com", "vidohd.com", "vidsource.me",
	"votrefile.xyz", "zidiplay.com", "fcdn.stream", "femax20.com", "sexhd.co", "mediashore.org", "viplayer.cc",
	"dutrag.com", "mrdhan.com", "embedsito.com", "diasfem.com", "superplayxyz.club", "albavido.xyz",
	"ncdnstm.com", "fembed-hd.com", "moviemaniac.org", "suzihaza.com", "fembed9hd.com", "vanfem.com",
	"fikper.com", "file.al", "fileaxa.com", "filecat.net", "filedot.xyz", "filedot.to", "filefactory.com",
	"filenext.com", "filer.net", "filerice.com", "filesfly.cc", "filespace.com", "filestore.me", "flashbit.cc",
	"dl.free.fr", "transfert.free.fr", "free.fr", "gigapeta.com", "gofile.io", "highload.to", "hitfile.net",
	"hitf.cc", "hulkshare.com", "icerbox.com", "isra.cloud", "goloady.com", "jumploads.com", "katfile.com",
	"k2s.cc", "keep2share.com", "keep2share.cc", "kshared.com", "load.to", "mediafile.cc", "mediafire.com",
	"mega.nz", "mega.co.nz", "mexa.sh", "mexashare.com", "mx-sh.net", "mixdrop.co", "mixdrop.to",
	"mixdrop.club", "mixdrop.sx", "modsbase.com", "nelion.me", "nitroflare.com", "nitro.download",
	"e.pcloud.link", "pixeldrain.com", "prefiles.com", "rg.to", "rapidgator.net", "rapidgator.asia",
	"scribd.com", "sendspace.com", "sharemods.com", "soundcloud.com", "noregx.debrid.link", "streamlare.com",
	"slmaxed.com", "sltube.org", "slwatch.co", "streamtape.com", "subyshare.com", "supervideo.tv",
	"terabox.com", "tezfiles.com", "turbobit.net", "turbobit.cc", "turbobit.pw", "turbobit.online",
	"turbobit.ru", "turbobit.live", "turbo.to", "turb.to", "turb.cc", "turbabit.com", "trubobit.com", "turb.pw",
	"turboblt.co", "turboget.net", "ubiqfile.com", "ulozto.net", "uloz.to", "zachowajto.pl", "ulozto.cz",
	"ulozto.sk", "upload-4ever.com", "up-4ever.com", "up-4ever.net", "uptobox.com", "uptostream.com",
	"uptobox.fr", "uptostream.fr", "uptobox.eu", "uptostream.eu", "uptobox.link", "uptostream.link",
	"upvid.pro", "upvid.live", "upvid.host", "upvid.co", "upvid.biz", "upvid.cloud", "opvid.org",
	"opvid.online", "uqload.com", "uqload.co", "uqload.io", "userload.co", "usersdrive.com", "vidoza.net",
	"voe.sx", "voe-unblock.com", "voeunblock1.com", "voeunblock2.com", "voeunblock3.com", "voeunbl0ck.com",
	"voeunblck.com", "voeunblk.com", "voe-un-block.com", "voeun-block.net", "reputationsheriffkennethsand.com",
	"449unceremoniousnasoseptal.com", "world-files.com", "worldbytez.com", "salefiles.com", "wupfile.com",
	"youdbox.com", "yodbox.com", "youtube.com", "youtu.be", "4tube.com", "academicearth.org", "acast.com",
	"add-anime.net", "air.mozilla.org", "allocine.fr", "alphaporno.com", "anysex.com", "aparat.com",
	"www.arte.tv", "video.arte.tv", "sites.arte.tv", "creative.arte.tv", "info.arte.tv", "future.arte.tv",
	"ddc.arte.tv", "concert.arte.tv", "cinema.arte.tv", "audi-mediacenter.com", "audioboom.com",
	"audiomack.com", "beeg.com", "camdemy.com", "chilloutzone.net", "clubic.com", "clyp.it", "daclips.in",
	"dailymail.co.uk", "www.dailymail.co.uk", "dailymotion.com", "touch.dailymotion.com", "democracynow.org",
	"discovery.com", "investigationdiscovery.com", "discoverylife.com", "animalplanet.com", "ahctv.com",
	"destinationamerica.com", "sciencechannel.com", "tlc.com", "velocity.com", "dotsub.com", "ebaumsworld.com",
	"eitb.tv", "ellentv.com", "ellentube.com", "flipagram.com", "footyroom.com", "formula1.com",
	"video.foxnews.com", "video.foxbusiness.com", "video.insider.foxnews.com", "franceculture.fr",
	"gameinformer.com", "gamersyde.com", "gorillavid.in", "hbo.com", "hellporno.com", "hentai.animestigma.com",
	"hornbunny.com", "imdb.com", "instagram.com", "itar-tass.com", "tass.ru", "jamendo.com", "jove.com",
	"keek.com", "k.to", "keezmovies.com", "khanacademy.org", "kickstarter.com", "krasview.ru", "la7.it",
	"lci.fr", "play.lcp.fr", "libsyn.com", "html5-player.libsyn.com", "liveleak.com", "livestream.com",
	"new.livestream.com", "m6.fr", "www.m6.fr", "metacritic.com", "mgoon.com", "m.mgoon.com", "mixcloud.com",
	"mojvideo.com", "movieclips.com", "movpod.in", "musicplayon.com", "myspass.de", "myvidster.com",
	"odatv.com", "onionstudios.com", "ora.tv", "unsafespeech.com", "play.fm", "plays.tv", "playvid.com",
	"pornhd.com", "pornhub.com", "www.pornhub.com", "pyvideo.org", "redtube.com", "embed.redtube.com",
	"www.redtube.com", "reverbnation.com", "revision3.com", "animalist.com", "seeker.com", "rts.ch", "rtve.es",
	"videos.sapo.pt", "videos.sapo.cv", "videos.sapo.ao", "videos.sapo.mz", "videos.sapo.tl", "sbs.com.au",
	"www.sbs.com.au", "screencast.com", "skysports.com", "slutload.com", "soundgasm.net",
	"store.steampowered.com", "steampowered.com", "steamcommunity.com", "stream.cz", "streamable.com",
	"streamcloud.eu", "sunporno.com", "teachertube.com", "teamcoco.com", "ted.com", "tfo.org", "thescene.com",
	"thesixtyone.com", "tnaflix.com", "trutv.com", "tu.tv", "turbo.fr", "tweakers.net", "ustream.tv",
	"vbox7.com", "veehd.com", "veoh.com", "vid.me", "videodetective.com", "vimeo.com", "vimeopro.com",
	"player.vimeo.com", "player.vimeopro.com", "wat.tv", "wimp.com", "xtube.com", "yahoo.com",
	"screen.yahoo.com", "news.yahoo.com", "sports.yahoo.com", "video.yahoo.com", "youporn.com",
}

var realDebridSites = []string{
	"1fichier.com", "2shared.com", "4shared.com", "alfafile.net", "anzfile.net", "backin.net", "bayfiles.com",
	"bdupload.in", "brupload.net", "btafile.com", "catshare.net", "clicknupload.me", "clipwatching.com",
	"cosmobox.org", "dailymotion.com", "dailyuploads.net", "daofile.com", "datafilehost.com", "ddownload.com",
	"depositfiles.com", "dl.free.fr", "douploads.net", "drop.download", "earn4files.com", "easybytez.com",
	"ex-load.com", "extmatrix.com", "down.fast-down.com", "fastclick.to", "faststore.org", "file.al",
	"file4safe.com", "fboom.me", "filefactory.com", "filefox.cc", "filenext.com", "filer.net", "filerio.in",
	"filesabc.com", "filespace.com", "file-up.org", "fileupload.pw", "filezip.cc", "fireget.com", "flashbit.cc",
	"flashx.tv", "florenfile.com", "fshare.vn", "gigapeta.com", "goloady.com", "docs.google.com",
	"gounlimited.to", "heroupload.com", "hexupload.net", "hitfile.net", "hotlink.cc", "hulkshare.com",
	"icerbox.com", "inclouddrive.com", "isra.cloud", "katfile.com", "keep2share.cc", "letsupload.cc", "load.to",
	"down.mdiaload.com", "mediafire.com", "mega.co.nz", "mixdrop.co", "mixloads.com", "mp4upload.com",
	"nelion.me", "ninjastream.to", "nitroflare.com", "nowvideo.club", "oboom.com", "prefiles.com", "sky.fm",
	"rapidgator.net", "rapidrar.com", "rapidu.net", "rarefile.net", "real-debrid.com", "redbunker.net",
	"redtube.com", "rockfile.eu", "rutube.ru", "scribd.com", "sendit.cloud", "sendspace.com",
	"simfileshare.net", "solidfiles.com", "soundcloud.com", "speed-down.org", "streamon.to", "streamtape.com",
	"takefile.link", "tezfiles.com", "thevideo.me", "turbobit.net", "tusfiles.com", "ubiqfile.com", "uloz.to",
	"unibytes.com", "uploadbox.io", "uploadboy.com", "uploadc.com", "uploaded.net", "uploadev.org",
	"uploadgig.com", "uploadrar.com", "uppit.com", "upstore.net", "upstream.to", "uptobox.com",
	"userscloud.com", "usersdrive.com", "vidcloud.ru", "videobin.co", "vidlox.tv", "vidoza.net", "vimeo.com",
	"vivo.sx", "vk.com", "voe.sx", "wdupload.com", "wipfiles.net", "world-files.com", "worldbytez.com",
	"wupfile.com", "wushare.com", "xubster.com", "youporn.com", "youtube.com",
}
