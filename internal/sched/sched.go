package sched

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"

	"butler/internal/config"
	"butler/internal/du"
	"butler/internal/monitor"
	"butler/internal/rss"
)

// Run starts one goroutine per configured task.
func Run(ctx context.Context, cfg *config.Config, st *monitor.State) {
	for _, t := range cfg.Tasks {
		go loop(ctx, t, st)
	}
}

func loop(ctx context.Context, t config.Task, st *monitor.State) {
	if t.EverySeconds <= 0 {
		t.EverySeconds = 60
	}
	// run once immediately, then on the interval
	if err := runTask(ctx, t, st); err != nil {
		log.Printf("[%s] error: %v", t.ID, err)
	}
	tick := time.NewTicker(time.Duration(t.EverySeconds) * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := runTask(ctx, t, st); err != nil {
				log.Printf("[%s] error: %v", t.ID, err)
			}
		}
	}
}

func runTask(ctx context.Context, t config.Task, st *monitor.State) error {
	switch t.Type {
	case "metrics":
		return runMetrics(t, st)
	case "exec":
		return runExec(ctx, t, st)
	case "http":
		return runHTTP(ctx, t, st)
	case "rss":
		return runRSS(ctx, t, st)
	case "diskusage":
		return runDiskUsage(t, st)
	default:
		return fmt.Errorf("unknown task type %q", t.Type)
	}
}

func runMetrics(t config.Task, st *monitor.State) error {
	var b strings.Builder

	if ps, err := cpu.Percent(0, false); err == nil && len(ps) > 0 {
		fmt.Fprintf(&b, "cpu=%3.1f%%\n", ps[0])
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		fmt.Fprintf(&b, "mem=%3.1f%% (%d/%d MB)\n",
			vm.UsedPercent, vm.Used/1024/1024, vm.Total/1024/1024)
	}
	if parts, err := disk.Partitions(true); err == nil {
		for _, p := range parts {
			u, err := disk.Usage(p.Mountpoint)
			if err != nil {
				continue
			}
			fmt.Fprintf(&b, "disk %s=%3.1f%% (%d/%d GB)\n",
				p.Mountpoint, u.UsedPercent,
				u.Used/1024/1024/1024, u.Total/1024/1024/1024)
		}
	}
	if ni, err := net.IOCounters(false); err == nil && len(ni) > 0 {
		n := ni[0]
		fmt.Fprintf(&b, "net_rx=%s\nnet_tx=%s\n", humanSize(n.BytesRecv), humanSize(n.BytesSent))
	}
	if hi, err := host.Info(); err == nil {
		fmt.Fprintf(&b, "uptime=%s\n", (time.Duration(hi.Uptime) * time.Second).Round(time.Second))
	}

	st.Set(t.ID, strings.TrimSpace(b.String()))
	return nil
}

func runExec(ctx context.Context, t config.Task, st *monitor.State) error {
	timeout := time.Duration(t.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, t.Command, t.Args...)
	cmd.Env = mergeEnv(t.Env)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()

	text := strings.TrimSpace(out.String())
	if text == "" {
		text = "(no output)"
	}
	st.Set(t.ID, text)
	log.Printf("[%s] done: %s", t.ID, text)
	return err
}

func runHTTP(ctx context.Context, t config.Task, st *monitor.State) error {
	if t.URL == "" {
		return fmt.Errorf("http task missing url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	st.Set(t.ID, fmt.Sprintf("status=%d %s", resp.StatusCode, strings.TrimSpace(string(body))))
	return nil
}

// rssSeen tracks already-seen entry links per task id, so we can report how
// many items are new since the previous poll. In-memory only (resets on restart).
var (
	rssSeenMu sync.Mutex
	rssSeen   = map[string]map[string]bool{}
)

func runRSS(ctx context.Context, t config.Task, st *monitor.State) error {
	if t.URL == "" {
		return fmt.Errorf("rss task missing url")
	}
	feed, err := rss.Fetch(ctx, t.URL, t.TimeoutSec)
	if err != nil {
		return err
	}

	limit := t.Limit
	if limit <= 0 {
		limit = 5
	}

	var b strings.Builder
	var lines []string
	newCount := 0

	rssSeenMu.Lock()
	s := rssSeen[t.ID]
	if s == nil {
		s = map[string]bool{}
		rssSeen[t.ID] = s
	}
	for _, item := range feed.Items {
		if len(lines) >= limit {
			break
		}
		key := item.Link
		if key == "" {
			key = item.ID
		}
		if key != "" && !s[key] {
			newCount++
			s[key] = true
		}
		lines = append(lines, fmt.Sprintf("%s | %s", item.Title, item.Link))
	}
	rssSeenMu.Unlock()

	fmt.Fprintf(&b, "feed=%s entries=%d new=%d", feed.Title, len(feed.Items), newCount)
	for _, l := range lines {
		fmt.Fprintf(&b, "\n- %s", l)
	}

	st.Set(t.ID, b.String())
	return nil
}

func runDiskUsage(t config.Task, st *monitor.State) error {
	if t.Path == "" {
		return fmt.Errorf("diskusage task missing path")
	}
	limit := t.Limit
	if limit <= 0 {
		limit = 12
	}
	items, err := du.Scan(t.Path, limit)
	if err != nil {
		return err
	}
	type row struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	rows := make([]row, 0, len(items))
	for _, it := range items {
		rows = append(rows, row{Name: it.Name, Size: it.Size})
	}
	b, _ := json.Marshal(map[string]any{"path": t.Path, "items": rows})
	st.Set(t.ID, string(b))
	log.Printf("[%s] diskusage scanned %s (%d entries)", t.ID, t.Path, len(rows))
	return nil
}

func mergeEnv(extra map[string]string) []string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.Index(kv, "="); i >= 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}
	for k, v := range extra {
		env[k] = v
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

func humanSize(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(n)/float64(div), "KMGT"[exp])
}
