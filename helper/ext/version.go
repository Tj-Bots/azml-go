package ext

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"azml-go/config"
)

// Bot version, like the Python bot (vMAJOR.MINOR.PATCH-STATE). Raise it with every update:
// PATCH for fixes, MINOR for new features, MAJOR for big changes.
const (
	verMajor = 2
	verMinor = 2
	verPatch = 0
	verState = "go"
)

// BotVersion: v2.1.0-go
func BotVersion() string {
	return fmt.Sprintf("v%d.%d.%d-%s", verMajor, verMinor, verPatch, verState)
}

var verRe = regexp.MustCompile(`ver(Major|Minor|Patch)\s*=\s*(\d+)`)

// LatestVersion reads this file on UPSTREAM_REPO / UPSTREAM_BRANCH ("" if unknown).
func LatestVersion() string {
	cfg := config.Get()
	m := regexp.MustCompile(`github\.com[/:]([^/]+)/([^/\s]+?)(?:\.git)?/?$`).FindStringSubmatch(strings.TrimSpace(cfg.UpstreamRepo))
	if m == nil {
		return ""
	}
	owner := m[1][strings.LastIndex(m[1], "@")+1:]
	branch := cfg.UpstreamBranch
	if branch == "" {
		branch = DefaultUpstreamBranch
	}
	c := &http.Client{Timeout: 15 * time.Second}
	resp, err := c.Get(fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/helper/ext/version.go", owner, m[2], branch))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ""
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	parts := map[string]string{}
	for _, x := range verRe.FindAllStringSubmatch(string(body), -1) {
		parts[x[1]] = x[2]
	}
	if len(parts) < 3 {
		return ""
	}
	return fmt.Sprintf("v%s.%s.%s-%s", parts["Major"], parts["Minor"], parts["Patch"], verState)
}

// CompareVersions: the REMARKS line of the Python bot.
func CompareVersions(cur, latest string) string {
	parse := func(v string) []int {
		v = strings.TrimPrefix(strings.SplitN(v, "-", 2)[0], "v")
		var out []int
		for _, p := range strings.Split(v, ".") {
			n, err := strconv.Atoi(p)
			if err != nil {
				return nil
			}
			out = append(out, n)
		}
		return out
	}
	a, b := parse(cur), parse(latest)
	if a == nil || b == nil {
		return "Can't check latest version right now"
	}
	for i := 0; i < 3 && i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return "New Version Update is Available! Check Now!"
		} else if a[i] > b[i] {
			return "Running a newer version than the repo"
		}
	}
	return "Already up to date with latest version"
}
