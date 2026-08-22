package rss

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Item is a single feed entry.
type Item struct {
	Title string
	Link  string
	ID    string
}

// Feed holds the parsed channel/feed plus its entries.
type Feed struct {
	Title string
	Items []Item
}

// Fetch downloads and parses an RSS 2.0 or Atom feed.
func Fetch(ctx context.Context, url string, timeoutSec int) (*Feed, error) {
	timeout := time.Duration(timeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml, */*")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bad status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4MB cap
	if err != nil {
		return nil, err
	}
	return parse(data)
}

func parse(data []byte) (*Feed, error) {
	// Try RSS 2.0 first.
	var r rssRoot
	if err := xml.Unmarshal(data, &r); err == nil && len(r.Channel.Items) > 0 {
		f := &Feed{Title: strings.TrimSpace(r.Channel.Title)}
		for _, it := range r.Channel.Items {
			link := strings.TrimSpace(it.Link)
			if link == "" {
				link = strings.TrimSpace(it.GUID)
			}
			f.Items = append(f.Items, Item{
				Title: strings.TrimSpace(it.Title),
				Link:  link,
				ID:    strings.TrimSpace(it.GUID),
			})
		}
		return f, nil
	}

	// Then try Atom.
	var a atomFeed
	if err := xml.Unmarshal(data, &a); err == nil && len(a.Entries) > 0 {
		f := &Feed{Title: strings.TrimSpace(a.Title)}
		for _, e := range a.Entries {
			link := ""
			for _, l := range e.Links {
				if l.Rel == "" || l.Rel == "alternate" {
					link = strings.TrimSpace(l.Href)
					break
				}
			}
			if link == "" && len(e.Links) > 0 {
				link = strings.TrimSpace(e.Links[0].Href)
			}
			f.Items = append(f.Items, Item{
				Title: strings.TrimSpace(e.Title),
				Link:  link,
				ID:    strings.TrimSpace(e.ID),
			})
		}
		return f, nil
	}

	return nil, fmt.Errorf("unrecognized feed format")
}

type rssRoot struct {
	Channel struct {
		Title string `xml:"title"`
		Items []struct {
			Title string `xml:"title"`
			Link  string `xml:"link"`
			GUID  string `xml:"guid"`
		} `xml:"item"`
	} `xml:"channel"`
}

type atomFeed struct {
	Title   string `xml:"title"`
	Entries []struct {
		Title string `xml:"title"`
		ID    string `xml:"id"`
		Links []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
	} `xml:"entry"`
}
