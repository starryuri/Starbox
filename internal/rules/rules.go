package rules

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"butler/internal/kb"
)

// Run evaluates enabled rules every 60s against the monitor snapshot across every
// store (guest + each account) and performs their actions, with a per-rule cooldown.
func Run(ctx context.Context, snap func() map[string]string, stores func() []*kb.Store) {
	last := map[string]int64{}
	tick := time.NewTicker(60 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for _, s := range stores() {
				runOnce(snap, s, last)
			}
		}
	}
}

func runOnce(snap func() map[string]string, kstore *kb.Store, last map[string]int64) {
	rules, err := kstore.List("rules")
	if err != nil {
		return
	}
	s := snap()
	now := time.Now().Unix()
	for _, r := range rules {
		d := r.Data
		if en, _ := d["enabled"].(bool); !en {
			continue
		}
		cond, _ := d["cond"].(string)
		if cond == "" || !condOk(cond, d, s) {
			continue
		}
		cooldown := int64(900)
		if c, ok := d["cooldown"].(float64); ok && c > 0 {
			cooldown = int64(c)
		}
		if lf, ok := last[r.ID]; ok && now-lf < cooldown {
			continue
		}
		last[r.ID] = now
		doAction(d, kstore)
	}
}

func condOk(cond string, d map[string]interface{}, snap map[string]string) bool {
	switch cond {
	case "cpu_high":
		return parseF(lineKV(snap["system_metrics"])["cpu"]) > parseF(str(d, "param"))
	case "mem_high":
		return parseF(lineKV(snap["system_metrics"])["mem"]) > parseF(str(d, "param"))
	case "disk_high":
		pct := parseF(str(d, "param"))
		re := regexp.MustCompile(`disk\s+.+?=\s*([\d.]+)%`)
		for _, line := range strings.Split(snap["system_metrics"], "\n") {
			if m := re.FindStringSubmatch(line); len(m) > 1 && parseF(m[1]) > pct {
				return true
			}
		}
		return false
	case "email_unread_gt":
		n := parseF(str(d, "param"))
		re := regexp.MustCompile(`unread=(\d+)`)
		for _, v := range snap {
			if m := re.FindStringSubmatch(v); len(m) > 1 && parseF(m[1]) > n {
				return true
			}
		}
		return false
	case "rss_keyword":
		kw := strings.ToLower(str(d, "param"))
		for k, v := range snap {
			if strings.HasPrefix(k, "rss_") && strings.Contains(strings.ToLower(v), kw) {
				return true
			}
		}
		return false
	}
	return false
}

func doAction(d map[string]interface{}, kstore *kb.Store) {
	action, _ := d["action"].(string)
	name, _ := d["name"].(string)
	title, _ := d["title"].(string)
	body, _ := d["body"].(string)
	if title == "" {
		title = name
	}
	switch action {
	case "add_note":
		_, _ = kstore.Add("notes", map[string]interface{}{"title": title, "content": body, "tags": ""})
	case "notify":
		_, _ = kstore.Add("notif", map[string]interface{}{"type": "规则", "title": title, "body": body, "unix": time.Now().Unix(), "read": false})
	}
}

func lineKV(t string) map[string]string {
	o := map[string]string{}
	for _, l := range strings.Split(t, "\n") {
		if i := strings.Index(l, "="); i > 0 {
			o[l[:i]] = l[i+1:]
		}
	}
	return o
}

func parseF(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

func num(d map[string]interface{}, k string) float64 {
	if v, ok := d[k].(float64); ok {
		return v
	}
	return 0
}

func str(d map[string]interface{}, k string) string {
	if v, ok := d[k].(string); ok {
		return v
	}
	return ""
}
