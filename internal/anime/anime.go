package anime

import (
	"crypto/tls"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UpcomingEp is one scheduled episode from AniList.
type UpcomingEp struct {
	MediaID  int    `json:"media_id"`
	Title    string `json:"title"`
	Episode  int    `json:"episode"`
	AiringAt int64  `json:"airing_at"`
}

// Result is one search candidate from AniList.
type Result struct {
	ID        int     `json:"id"`
	Title     string  `json:"title"`
	TitleJP   string  `json:"title_japanese"`
	Episodes  *int    `json:"episodes"`
	Score     float64 `json:"score"`
	Cover     string  `json:"cover"`
	Synopsis  string  `json:"synopsis"`
	Year      int     `json:"year"`
	Status    string  `json:"status"`
	URL       string  `json:"url"`
	StartDate string  `json:"start_date"`
	Duration  int     `json:"duration"`
}

// Search queries the AniList GraphQL API by keyword and returns candidates.
func Search(q string) ([]Result, error) {
	query := `query ($q: String) { Page(perPage:8) { media(search:$q, type:ANIME) {
		id title{romaji english native} coverImage{extraLarge} episodes averageScore duration
		startDate{year month day} status description siteUrl } } }`
	payload, _ := json.Marshal(map[string]any{
		"query":     query,
		"variables": map[string]any{"q": q},
	})

	req, err := http.NewRequest(http.MethodPost, "https://graphql.anilist.co", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "starbox/0.1")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bad status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	var raw struct {
		Data struct {
			Page struct {
				Media []struct {
					ID       int    `json:"id"`
					Episodes *int   `json:"episodes"`
					Score    int    `json:"averageScore"`
					Status   string `json:"status"`
					SiteURL  string `json:"siteUrl"`
					Title    struct {
						Romaji string `json:"romaji"`
						Eng    string `json:"english"`
						Native string `json:"native"`
					} `json:"title"`
					Cover struct {
						ExtraLarge string `json:"extraLarge"`
					} `json:"coverImage"`
					Duration int `json:"duration"`
					StartDate struct {
						Year  int `json:"year"`
						Month int `json:"month"`
						Day   int `json:"day"`
					} `json:"startDate"`
					Description string `json:"description"`
				} `json:"media"`
			} `json:"Page"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}

	out := make([]Result, 0, len(raw.Data.Page.Media))
	for _, d := range raw.Data.Page.Media {
		title := d.Title.Eng
		if title == "" {
			title = d.Title.Romaji
		}
		out = append(out, Result{
			ID:        d.ID,
			Title:     title,
			TitleJP:   d.Title.Native,
			Episodes:  d.Episodes,
			Score:     float64(d.Score) / 10.0,
			Cover:     d.Cover.ExtraLarge,
			Synopsis:  d.Description,
			Year:      d.StartDate.Year,
			Status:    d.Status,
			URL:       d.SiteURL,
			StartDate: fmtDate(d.StartDate.Year, d.StartDate.Month, d.StartDate.Day),
			Duration:  d.Duration,
		})
	}
	return out, nil
}

// Upcoming queries AniList's airing schedule for the given media IDs and
// returns episodes that have not yet aired, sorted by air time.
func Upcoming(ids []int) ([]UpcomingEp, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	query := `query ($ids: [Int]) { Page(perPage:50) { airingSchedules(mediaId_in:$ids, notYetAired:true, sort:TIME) { airingAt episode media { id title { romaji english } } } } }`
	payload, _ := json.Marshal(map[string]any{
		"query":     query,
		"variables": map[string]any{"ids": ids},
	})
	req, err := http.NewRequest(http.MethodPost, "https://graphql.anilist.co", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "starbox/0.1")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bad status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	var raw struct {
		Data struct {
			Page struct {
				Airing []struct {
					AiringAt int64 `json:"airingAt"`
					Episode  int  `json:"episode"`
					Media    struct {
						ID    int `json:"id"`
						Title struct {
							Romaji string `json:"romaji"`
							Eng    string `json:"english"`
						} `json:"title"`
					} `json:"media"`
				} `json:"airingSchedules"`
			} `json:"Page"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make([]UpcomingEp, 0, len(raw.Data.Page.Airing))
	for _, a := range raw.Data.Page.Airing {
		title := a.Media.Title.Eng
		if title == "" {
			title = a.Media.Title.Romaji
		}
		out = append(out, UpcomingEp{MediaID: a.Media.ID, Title: title, Episode: a.Episode, AiringAt: a.AiringAt})
	}
	return out, nil
}

// Detail holds rich anime info: studios, characters and their voice actors.
type Detail struct {
	ID         int         `json:"id"`
	Title      string      `json:"title"`
	TitleJP    string      `json:"title_jp"`
	Cover      string      `json:"cover"`
	Synopsis   string      `json:"synopsis"`
	Episodes   *int        `json:"episodes"`
	Score      float64     `json:"score"`
	Year       int         `json:"year"`
	Status     string      `json:"status"`
	URL        string      `json:"url"`
	StartDate  string      `json:"start_date"`
	Duration   int         `json:"duration"`
	Studios    []Studio    `json:"studios"`
	Characters []Character `json:"characters"`
	Relations  []Relation  `json:"relations"`
}

// Relation is a linked entry of the same series (sequel/prequel/etc).
type Relation struct {
	ID      int    `json:"id"`
	Title   string `json:"title"`
	TitleJP string `json:"title_jp"`
	Kind    string `json:"kind"`
}

type Studio struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Character struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	NameNative string `json:"name_native"`
	Role       string `json:"role"`
	VAs        []VA   `json:"vas"`
}

type VA struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	NameNative string `json:"name_native"`
}

// Detail fetches full anime info from AniList (studios + cast).
func GetDetail(id int) (Detail, error) {
	query := `query($id:Int){Media(id:$id,type:ANIME){id title{romaji english native} coverImage{extraLarge} description episodes averageScore duration startDate{year month day} status siteUrl studios(isMain:true){nodes{id name}} characters(sort:ROLE,perPage:30){edges{role node{id name{full native}} voiceActors(language:JAPANESE){id name{full native}}}} relations{edges{relationType node{id title{english romaji native}}}}}}`
	payload, _ := json.Marshal(map[string]any{"query": query, "variables": map[string]any{"id": id}})
	req, _ := http.NewRequest(http.MethodPost, "https://graphql.anilist.co", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "starbox/0.1")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Detail{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return Detail{}, fmt.Errorf("bad status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var raw struct {
		Data struct {
			Media struct {
				ID       int    `json:"id"`
				Episodes *int   `json:"episodes"`
				Score    int    `json:"averageScore"`
				Status   string `json:"status"`
				SiteURL  string `json:"siteUrl"`
				Title    struct {
					Romaji string `json:"romaji"`
					Eng    string `json:"english"`
					Native string `json:"native"`
				} `json:"title"`
				Cover   struct {
					ExtraLarge string `json:"extraLarge"`
				} `json:"coverImage"`
				Duration int `json:"duration"`
				StartDate struct {
					Year  int `json:"year"`
					Month int `json:"month"`
					Day   int `json:"day"`
				} `json:"startDate"`
				Description string `json:"description"`
				Relations struct {
					Edges []struct {
						RelationType string `json:"relationType"`
						Node         struct {
							ID    int `json:"id"`
							Title struct {
								Eng    string `json:"english"`
								Romaji string `json:"romaji"`
								Native string `json:"native"`
							} `json:"title"`
						} `json:"node"`
					} `json:"edges"`
				} `json:"relations"`
				Studios     struct {
					Nodes []struct {
						ID   int    `json:"id"`
						Name string `json:"name"`
					} `json:"nodes"`
				} `json:"studios"`
				Characters struct {
					Edges []struct {
						Role   string `json:"role"`
						Node   struct {
							ID   int    `json:"id"`
							Name struct {
								Full   string `json:"full"`
								Native string `json:"native"`
							} `json:"name"`
						} `json:"node"`
						VAs []struct {
							ID   int    `json:"id"`
							Name struct {
								Full   string `json:"full"`
								Native string `json:"native"`
							} `json:"name"`
						} `json:"voiceActors"`
					} `json:"edges"`
				} `json:"characters"`
			} `json:"Media"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Detail{}, err
	}
	m := raw.Data.Media
	title := m.Title.Eng
	if title == "" {
		title = m.Title.Romaji
	}
	d := Detail{
		ID: m.ID, Title: title, TitleJP: m.Title.Native, Cover: m.Cover.ExtraLarge,
		Synopsis: m.Description, Episodes: m.Episodes, Score: float64(m.Score) / 10.0,
		Year: m.StartDate.Year, Status: m.Status, URL: m.SiteURL,
		StartDate: fmtDate(m.StartDate.Year, m.StartDate.Month, m.StartDate.Day), Duration: m.Duration,
	}
	for _, re := range m.Relations.Edges {
		rt := re.RelationType
		switch rt {
		case "SEQUEL", "PREQUEL", "SIDE_STORY", "PARENT", "ALTERNATIVE":
		default:
			continue
		}
		rtitle := re.Node.Title.Native
		if rtitle == "" {
			rtitle = re.Node.Title.Eng
		}
		if rtitle == "" {
			rtitle = re.Node.Title.Romaji
		}
		d.Relations = append(d.Relations, Relation{ID: re.Node.ID, Title: rtitle, TitleJP: re.Node.Title.Native, Kind: rt})
	}
	for _, s := range m.Studios.Nodes {
		d.Studios = append(d.Studios, Studio{ID: s.ID, Name: s.Name})
	}
	for _, e := range m.Characters.Edges {
		c := Character{ID: e.Node.ID, Name: e.Node.Name.Full, NameNative: e.Node.Name.Native, Role: e.Role}
		for _, va := range e.VAs {
			c.VAs = append(c.VAs, VA{ID: va.ID, Name: va.Name.Full, NameNative: va.Name.Native})
		}
		d.Characters = append(d.Characters, c)
	}
	return d, nil
}

// Works bundles an entity (studio or voice actor) with its anime credits.
type Works struct {
	ID    int     `json:"id"`
	Name  string  `json:"name"`
	Image string  `json:"image"`
	Media []Media `json:"media"`
}

// Media is a lightweight anime summary used in a works list.
type Media struct {
	ID      int     `json:"id"`
	Title   string  `json:"title"`
	TitleJP string  `json:"title_jp"`
	Cover   string  `json:"cover"`
	Score   float64 `json:"score"`
	Year    int     `json:"year"`
	Status  string  `json:"status"`
	Format  string  `json:"format"`
}

// GetStudio fetches a production studio and its anime works from AniList.
func GetStudio(id int) (Works, error) {
	query := `query($id:Int){Studio(id:$id){id name media(page:1 perPage:24 sort:START_DATE_DESC){nodes{id title{romaji english native} coverImage{extraLarge} averageScore startDate{year} status format}}}}`
	var raw struct {
		Data struct {
			Studio struct {
				ID    int    `json:"id"`
				Name  string `json:"name"`
				Media struct {
					Nodes []RawMedia `json:"nodes"`
				} `json:"media"`
			} `json:"Studio"`
		} `json:"data"`
	}
	if err := anilistQuery(query, map[string]any{"id": id}, &raw); err != nil {
		return Works{}, err
	}
	ws := Works{ID: raw.Data.Studio.ID, Name: raw.Data.Studio.Name, Media: parseMedia(raw.Data.Studio.Media.Nodes)}
	return ws, nil
}

// GetStaff fetches a voice actor (staff) and their credited anime from AniList.
func GetStaff(id int) (Works, error) {
	query := `query($id:Int){Staff(id:$id){id name{full} image{large} staffMedia(page:1 perPage:24 sort:START_DATE_DESC){nodes{id title{romaji english native} coverImage{extraLarge} averageScore startDate{year} status format}}}}`
	var raw struct {
		Data struct {
			Staff struct {
				ID         int    `json:"id"`
				Name       struct {
					Full string `json:"full"`
				} `json:"name"`
				Image      struct {
					Large string `json:"large"`
				} `json:"image"`
				StaffMedia struct {
					Nodes []RawMedia `json:"nodes"`
				} `json:"staffMedia"`
			} `json:"Staff"`
		} `json:"data"`
	}
	if err := anilistQuery(query, map[string]any{"id": id}, &raw); err != nil {
		return Works{}, err
	}
	ws := Works{ID: raw.Data.Staff.ID, Name: raw.Data.Staff.Name.Full, Image: raw.Data.Staff.Image.Large, Media: parseMedia(raw.Data.Staff.StaffMedia.Nodes)}
	return ws, nil
}

// RawMedia models the shared AniList media summary used by studio/staff queries.
type RawMedia struct {
	ID   int `json:"id"`
	Title struct {
		Romaji string `json:"romaji"`
		Eng    string `json:"english"`
		Native string `json:"native"`
	} `json:"title"`
	Cover struct {
		ExtraLarge string `json:"extraLarge"`
	} `json:"coverImage"`
	AverageScore int    `json:"averageScore"`
	StartDate    struct {
		Year int `json:"year"`
	} `json:"startDate"`
	Status string `json:"status"`
	Format string `json:"format"`
}

// parseMedia converts raw AniList media nodes into the compact Media summary.
func parseMedia(nodes []RawMedia) []Media {
	out := make([]Media, 0, len(nodes))
	for _, m := range nodes {
		t := m.Title.Eng
		if t == "" {
			t = m.Title.Romaji
		}
		out = append(out, Media{
			ID: m.ID, Title: t, TitleJP: m.Title.Native, Cover: m.Cover.ExtraLarge,
			Score: float64(m.AverageScore) / 10.0, Year: m.StartDate.Year,
			Status: m.Status, Format: m.Format,
		})
	}
	return out
}

// anilistQuery posts a GraphQL query to AniList and decodes into out.
func anilistQuery(query string, variables map[string]any, out interface{}) error {
	payload, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
	req, err := http.NewRequest(http.MethodPost, "https://graphql.anilist.co", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "starbox/0.1")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("bad status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return json.Unmarshal(body, out)
}

// MoegirlHit is one 萌娘百科 search result.
type MoegirlHit struct {
	Title   string `json:"title"`
	PageID  int    `json:"pageid"`
	Snippet string `json:"snippet"`
	URL     string `json:"url"`
}

// moegirlUA is a browser-ish UA so 萌娘百科's anonymous MediaWiki API accepts the call.
const moegirlUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"

// MoegirlSearch queries 萌娘百科's opensearch endpoint for Chinese title suggestions.
// Note: list=search is gated behind authorization, but opensearch is open.
func MoegirlSearch(q string) ([]MoegirlHit, error) {
	u := "https://zh.moegirl.org.cn/api.php?action=opensearch&search=" + url.QueryEscape(q) + "&limit=8&namespace=0&format=json"
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", moegirlUA)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bad status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	// opensearch returns [query, titles[], descriptions[], urls[]]
	var raw []json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil || len(raw) < 2 {
		return nil, fmt.Errorf("opensearch: unexpected response")
	}
	var titles, urls []string
	_ = json.Unmarshal(raw[1], &titles)
	if len(raw) > 3 {
		_ = json.Unmarshal(raw[3], &urls)
	}
	out := make([]MoegirlHit, 0, len(titles))
	for i, t := range titles {
		u2 := "https://zh.moegirl.org.cn/" + url.PathEscape(t)
		if i < len(urls) && urls[i] != "" {
			u2 = urls[i]
		}
		out = append(out, MoegirlHit{Title: t, URL: u2})
	}
	return out, nil
}

// MoegirlItem is one fetched detail (Chinese cover + plain-text summary) for a moegirl title.
type MoegirlItem struct {
	Title   string `json:"title"`
	Cover   string `json:"cover"`
	Summary string `json:"summary"`
	URL     string `json:"url"`
}

// fmtDate builds a YYYY-MM-DD string from date parts (empty if unknown).
func fmtDate(y, m, d int) string {
	if y == 0 {
		return ""
	}
	if m == 0 || d == 0 {
		return fmt.Sprintf("%04d", y)
	}
	return fmt.Sprintf("%04d-%02d-%02d", y, m, d)
}

// cleanSummary strips trailing section markers and trims a MediaWiki plain-text extract.
func cleanSummary(s string) string {
	s = regexp.MustCompile(`(?s)==.*`).ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	if len(s) > 900 {
		s = s[:900] + "…"
	}
	return s
}

// MoegirlDetails fetches Chinese cover + summary for one or more moegirl titles via pageimages|extracts.
func MoegirlDetails(titles []string) ([]MoegirlItem, error) {
	if len(titles) == 0 {
		return nil, nil
	}
	if len(titles) > 10 {
		titles = titles[:10]
	}
	u := "https://zh.moegirl.org.cn/api.php?action=query&prop=pageimages|extracts&exintro=1&explaintext=1&pithumbsize=600&format=json&titles=" + url.QueryEscape(strings.Join(titles, "|"))
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", moegirlUA)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bad status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var raw struct {
		Query struct {
			Pages map[string]struct {
				Title     string `json:"title"`
				Extract   string `json:"extract"`
				Thumbnail struct {
					Source string `json:"source"`
				} `json:"thumbnail"`
				Missing bool `json:"missing"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make([]MoegirlItem, 0, len(raw.Query.Pages))
	for _, p := range raw.Query.Pages {
		if p.Missing {
			continue
		}
		out = append(out, MoegirlItem{Title: p.Title, Cover: p.Thumbnail.Source, Summary: cleanSummary(p.Extract), URL: "https://zh.moegirl.org.cn/" + url.PathEscape(p.Title)})
	}
	return out, nil
}

// BangumiResult is one Chinese search hit from bangumi (type=anime).
type BangumiResult struct {
	ID    int     `json:"id"`
	Title string  `json:"title"`
	Cover string  `json:"cover"`
	Score float64 `json:"score"`
	Year  int     `json:"year"`
	Eps   *int    `json:"eps"`
	URL   string  `json:"url"`
}

// BangumiSearch queries bangumi's v0 search API for anime (subject type 2).
// Note: api.bgm.tv may be unreachable on some networks; callers should fall back.
// bgmSearchItem is one result of bangumi's legacy subject search (via relay).
type bgmSearchItem struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	NameCN  string `json:"name_cn"`
	AirDate string `json:"air_date"`
	Eps     *int   `json:"eps"`
	Rating  struct {
		Score float64 `json:"score"`
	} `json:"rating"`
	Images struct {
		Large  string `json:"large"`
		Common string `json:"common"`
	} `json:"images"`
}

// BangumiSearch searches bangumi via the relay: GET /search/subject/{q}?type=2.
func BangumiSearch(q string) ([]BangumiResult, error) {
	u := bangumiBase + "/search/subject/" + url.PathEscape(q) + "?type=2"
	body, err := bgmGetRaw(u)
	if err != nil {
		return nil, err
	}
	var arr []bgmSearchItem
	if err := json.Unmarshal(body, &arr); err != nil || len(arr) == 0 {
		var box struct {
			List []bgmSearchItem `json:"list"`
		}
		if err := json.Unmarshal(body, &box); err == nil {
			arr = box.List
		}
	}
	out := make([]BangumiResult, 0, len(arr))
	for _, d := range arr {
		t := d.NameCN
		if t == "" {
			t = d.Name
		}
		year := 0
		if len(d.AirDate) >= 4 {
			year, _ = strconv.Atoi(d.AirDate[:4])
		}
		cover := d.Images.Large
		if cover == "" {
			cover = d.Images.Common
		}
		out = append(out, BangumiResult{ID: d.ID, Title: t, Cover: cover, Score: d.Rating.Score, Year: year, Eps: d.Eps, URL: "https://bgm.tv/subject/" + strconv.Itoa(d.ID)})
	}
	return out, nil
}

// BangumiCollection is one item in a Bangumi user's anime collection.
type BangumiCollection struct {
	SubjectID int     `json:"subject_id"`
	Title     string  `json:"title"`
	TitleCN   string  `json:"title_cn"`
	Cover     string  `json:"cover"`
	Score     float64 `json:"score"`
	Eps       int     `json:"eps"`
	Date      string  `json:"date"`
	Status    int     `json:"ep_status"`
	Type      int     `json:"type"`
	URL       string  `json:"url"`
}

// BangumiUserCollection fetches a user's Bangumi anime collection.
// typ: 1想看 2看过 3在看 4搁置 5抛弃. Mirrors api.bgm.tv/v0/users/{id}/collections.
func BangumiUserCollection(userID string, typ, limit, offset int) ([]BangumiCollection, error) {
	if limit <= 0 {
		limit = 50
	}
	u := bangumiBase + "/v0/users/" + url.PathEscape(userID) + "/collections?subject_type=2&type=" + strconv.Itoa(typ) + "&limit=" + strconv.Itoa(limit) + "&offset=" + strconv.Itoa(offset)
	var raw struct {
		Data []struct {
			SubjectID int `json:"subject_id"`
			Type      int `json:"type"`
			Rate      int `json:"rate"`
			EpStatus  int `json:"ep_status"`
			Subject   struct {
				ID     int    `json:"id"`
				Name   string `json:"name"`
				NameCN string `json:"name_cn"`
				Eps    int    `json:"eps"`
				Date   string `json:"date"`
				Images struct {
					Large  string `json:"large"`
					Medium string `json:"medium"`
					Common string `json:"common"`
				} `json:"images"`
			} `json:"subject"`
		} `json:"data"`
	}
	if err := bgmGet(u, &raw); err != nil {
		return nil, err
	}
	out := make([]BangumiCollection, 0, len(raw.Data))
	for _, d := range raw.Data {
		cover := d.Subject.Images.Large
		if cover == "" {
			cover = d.Subject.Images.Common
		}
		out = append(out, BangumiCollection{SubjectID: d.SubjectID, Title: d.Subject.Name, TitleCN: d.Subject.NameCN, Cover: cover, Score: float64(d.Rate) / 10.0, Eps: d.Subject.Eps, Date: d.Subject.Date, Status: d.EpStatus, Type: d.Type, URL: "https://bgm.tv/subject/" + strconv.Itoa(d.SubjectID)})
	}
	return out, nil
}

// BangumiPerson is a person/studio credited on a subject.
type BangumiPerson struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Relation string `json:"relation"`
	Type     int    `json:"type"`
}

// BangumiSubjectPersons fetches credited persons/studios (relation=动画制作/配音 etc) for a subject.
func BangumiSubjectPersons(subjectID int) ([]BangumiPerson, error) {
	u := bangumiBase + "/v0/subjects/" + strconv.Itoa(subjectID) + "/persons"
	var raw []struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Relation string `json:"relation"`
		Type     int    `json:"type"`
	}
	if err := bgmGet(u, &raw); err != nil {
		return nil, err
	}
	out := make([]BangumiPerson, 0, len(raw))
	for _, p := range raw {
		out = append(out, BangumiPerson{ID: p.ID, Name: p.Name, Relation: p.Relation, Type: p.Type})
	}
	return out, nil
}

// BangumiSubject is a bangumi subject detail (Chinese metadata).
type BangumiSubject struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	NameCN  string `json:"name_cn"`
	Summary string `json:"summary"`
	Date    string `json:"date"`
	Eps     *int   `json:"eps"`
	Rating  struct {
		Score float64 `json:"score"`
	} `json:"rating"`
	Images struct {
		Large  string `json:"large"`
		Common string `json:"common"`
	} `json:"images"`
	URL string `json:"url"`
}

// BangumiGetDetail fetches a subject's Chinese detail via the relay.
func BangumiGetDetail(id int) (BangumiSubject, error) {
	u := bangumiBase + "/v0/subjects/" + strconv.Itoa(id)
	body, err := bgmGetRaw(u)
	if err != nil {
		return BangumiSubject{}, err
	}
	var s BangumiSubject
	if err := json.Unmarshal(body, &s); err != nil {
		return BangumiSubject{}, err
	}
	s.URL = "https://bgm.tv/subject/" + strconv.Itoa(s.ID)
	return s, nil
}

// BangumiCharacter is a character with its voice actors (from bangumi).
type BangumiCharacter struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Relation string `json:"relation"`
	Actors   []struct {
		Name   string   `json:"name"`
		Career []string `json:"career"`
	} `json:"actors"`
}

// BangumiCharactersGet fetches a subject's characters + voice actors via the relay.
func BangumiCharactersGet(id int) ([]BangumiCharacter, error) {
	u := bangumiBase + "/v0/subjects/" + strconv.Itoa(id) + "/characters"
	body, err := bgmGetRaw(u)
	if err != nil {
		return nil, err
	}
	var arr []BangumiCharacter
	if err := json.Unmarshal(body, &arr); err != nil || len(arr) == 0 {
		var box struct {
			Data []BangumiCharacter `json:"data"`
		}
		if err := json.Unmarshal(body, &box); err == nil {
			arr = box.Data
		}
	}
	return arr, nil
}

// bgmGet does a GET against api.bgm.tv with a browser-ish UA.
func bgmGet(u string, out interface{}) error {
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", moegirlUA)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("bangumi bad status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return json.Unmarshal(body, out)
}

const bangumiBase = "https://bgmapi.anibt.net"

func bgmGetRaw(u string) ([]byte, error) {
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", moegirlUA)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bangumi bad status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

const xinyuuBase = "https://db.xinyuu.cn/api"

// XinyuuAnime is one anime record from XinyuuDB (Chinese metadata).
type XinyuuAnime struct {
	AnimeID           int    `json:"anime_id"`
	TitleOriginal     string `json:"title_original"`
	TitleChinese      string `json:"title_chinese"`
	TitleOtherAliases string `json:"title_other_aliases"`
	Description       string `json:"description"`
	TotalEpisodes     interface{} `json:"total_episodes"`
	Year              interface{} `json:"year"`
	Quarter           interface{} `json:"quarter"`
	SeasonVersion     interface{} `json:"season_version"`
	SeasonReleaseDate string `json:"season_release_date"`
	OriginalWorkType  string `json:"original_work_type"`
	OfficialWebsite   string `json:"official_website"`
	CoverImage        string `json:"cover_image"`
	URL               string `json:"url"`
}

// XinyuuStaff is a credited person/studio from XinyuuDB (Chinese names).
type XinyuuStaff struct {
	StaffID      int    `json:"staff_id"`
	NameChinese  string `json:"name_chinese"`
	NameOriginal string `json:"name_original"`
	RoleType     string `json:"role_type"`
	IsMainStaff  int    `json:"is_main_staff"`
}

// XinyuuCharacter is a character with its Chinese voice actors from XinyuuDB.
type XinyuuCharacter struct {
	CharacterID  int      `json:"character_id"`
	NameChinese  string   `json:"name_chinese"`
	CharacterType string  `json:"character_type"`
	VoiceActors  []string `json:"voice_actors"`
}

// XinyuuDetail bundles anime metadata + staff + characters.
type XinyuuDetail struct {
	Anime      XinyuuAnime      `json:"anime"`
	Staff      []XinyuuStaff    `json:"staff"`
	Characters []XinyuuCharacter `json:"characters"`
}

// xinyuuTransport tolerates the incomplete TLS chain db.xinyuu.cn serves to
// some Windows clients (read-only public metadata API, no credentials sent).
var xinyuuTransport = func() http.RoundTripper {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	return tr
}()

func xinyuuGet(path string, out interface{}) error {
	req, _ := http.NewRequest(http.MethodGet, xinyuuBase+path, nil)
	req.Header.Set("User-Agent", moegirlUA)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 15 * time.Second, Transport: xinyuuTransport}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("xinyuu bad status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return json.Unmarshal(body, out)
}

// XinyuuSearch searches XinyuuDB by keyword (Chinese-friendly).
func XinyuuSearch(q string) ([]XinyuuAnime, error) {
	var raw struct {
		Code int            `json:"code"`
		Data []XinyuuAnime `json:"data"`
	}
	if err := xinyuuGet("/animes/search?q="+url.QueryEscape(q), &raw); err != nil {
		return nil, err
	}
	for i := range raw.Data {
		raw.Data[i].URL = "https://db.xinyuu.cn/"
	}
	return raw.Data, nil
}

// XinyuuAnimeGet fetches one anime's metadata by id.
func XinyuuAnimeGet(id int) (XinyuuAnime, error) {
	var raw struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := xinyuuGet("/animes/"+strconv.Itoa(id), &raw); err != nil {
		return XinyuuAnime{}, err
	}
	var a XinyuuAnime
	_ = json.Unmarshal(raw.Data, &a)
	if a.AnimeID == 0 {
		var box struct {
			Data []XinyuuAnime `json:"data"`
		}
		_ = json.Unmarshal(raw.Data, &box)
		if len(box.Data) > 0 {
			a = box.Data[0]
		}
	}
	if a.AnimeID == 0 {
		return XinyuuAnime{}, fmt.Errorf("xinyuu anime %d not found", id)
	}
	a.URL = "https://db.xinyuu.cn/"
	return a, nil
}

// XinyuuStaffGet fetches credited staff for an anime.
func XinyuuStaffGet(id int) ([]XinyuuStaff, error) {
	var raw struct {
		Code int            `json:"code"`
		Data []XinyuuStaff `json:"data"`
	}
	if err := xinyuuGet("/animes/"+strconv.Itoa(id)+"/staff", &raw); err != nil {
		return nil, err
	}
	return raw.Data, nil
}

// XinyuuCharactersGet fetches characters + voice actors for an anime.
func XinyuuCharactersGet(id int) ([]XinyuuCharacter, error) {
	var raw struct {
		Code int                `json:"code"`
		Data []XinyuuCharacter `json:"data"`
	}
	if err := xinyuuGet("/animes/"+strconv.Itoa(id)+"/characters", &raw); err != nil {
		return nil, err
	}
	return raw.Data, nil
}

// GetXinyuuDetail fetches combined Chinese data for an anime.
func GetXinyuuDetail(id int) (XinyuuDetail, error) {
	a, err := XinyuuAnimeGet(id)
	if err != nil {
		return XinyuuDetail{}, err
	}
	st, _ := XinyuuStaffGet(id)
	ch, _ := XinyuuCharactersGet(id)
	return XinyuuDetail{Anime: a, Staff: st, Characters: ch}, nil
}

// XinyuuStaffWork is one anime credited to a staff/person (Chinese works list).
type XinyuuStaffWork struct {
	AnimeID          int         `json:"anime_id"`
	TitleOriginal    string      `json:"title_original"`
	TitleChinese     string      `json:"title_chinese"`
	OriginalWorkType string      `json:"original_work_type"`
	TotalEpisodes    interface{} `json:"total_episodes"`
	IsOngoing        int         `json:"is_ongoing"`
	CoverImage       string      `json:"cover_image"`
	BgmID            int         `json:"bgm_id"`
	Year             interface{} `json:"year"`
	Quarter          interface{} `json:"quarter"`
	SeasonReleaseDate string     `json:"season_release_date"`
	Description      string      `json:"description"`
	URL              string      `json:"url"`
}

// XinyuuStaffSearch finds staff by keyword (by name).
func XinyuuStaffSearch(q string) ([]XinyuuStaff, error) {
	var raw struct {
		Code int            `json:"code"`
		Data []XinyuuStaff `json:"data"`
	}
	if err := xinyuuGet("/staff/search?q="+url.QueryEscape(q), &raw); err != nil {
		return nil, err
	}
	return raw.Data, nil
}

// xyMeCache caches XinyuuDB anime records keyed by anime_id so repeated
// studio works lookups do not re-search the network for covers.
var xyMeCache sync.Map // anime_id -> XinyuuAnime (Chinese metadata: year/quarter/description)
var xyCoverCache sync.Map // anime_id -> workCover (reachable cover + bgm_id)

// workCover holds a reachable cover and its Bangumi subject id.
type workCover struct {
	cover string
	bgmID int
}

// httpsURL forces a scheme of https for CDN cover hosts (the page may be served over https).
func httpsURL(u string) string {
	if strings.HasPrefix(u, "http://") {
		return "https://" + strings.TrimPrefix(u, "http://")
	}
	return u
}

// bgmCover searches Bangumi (Chinese-first) for a reachable cover + subject id.
func bgmCover(t string) (string, int) {
	if t == "" {
		return "", 0
	}
	res, err := BangumiSearch(t)
	if err != nil {
		return "", 0
	}
	for _, b := range res {
		if b.Cover != "" {
			return httpsURL(b.Cover), b.ID
		}
	}
	return "", 0
}

// anilistCover searches AniList as a fallback cover provider.
func anilistCover(t string) (string, int) {
	if t == "" {
		return "", 0
	}
	res, err := Search(t)
	if err != nil {
		return "", 0
	}
	for _, a := range res {
		if a.Cover != "" {
			return a.Cover, a.ID
		}
	}
	return "", 0
}

// reachableCover tries Bangumi (Chinese-first) then AniList for a cover that
// actually loads (XinyuuDB's own cover host is often unreachable).
func reachableCover(chinese, original string) workCover {
	if c, id := bgmCover(chinese); c != "" {
		return workCover{c, id}
	}
	if c, id := bgmCover(original); c != "" {
		return workCover{c, id}
	}
	if c, id := anilistCover(chinese); c != "" {
		return workCover{c, id}
	}
	if c, id := anilistCover(original); c != "" {
		return workCover{c, id}
	}
	return workCover{}
}

// xySearchAnimeByTitle finds the exact anime_id among search results for a title.
func xySearchAnimeByTitle(title string, id int) (XinyuuAnime, bool) {
	if title == "" {
		return XinyuuAnime{}, false
	}
	res, err := XinyuuSearch(title)
	if err != nil {
		return XinyuuAnime{}, false
	}
	for _, x := range res {
		if x.AnimeID == id {
			return x, true
		}
	}
	return XinyuuAnime{}, false
}

// applyXYMeta copies the description/total/year fields from an enriched record.
func applyXYMeta(work *XinyuuStaffWork, m XinyuuAnime) {
	if m.Year != nil {
		work.Year = m.Year
	}
	if m.Quarter != nil {
		work.Quarter = m.Quarter
	}
	if work.TotalEpisodes == nil {
		work.TotalEpisodes = m.TotalEpisodes
	}
	if work.Description == "" {
		work.Description = m.Description
	}
}

// XinyuuStaffAnimes lists the works credited to a staff member (studio/CV).
// Each work is enriched with Chinese metadata plus a reachable cover (Bangumi
// first, AniList fallback) because XinyuuDB's own cover host is unreliable.
func XinyuuStaffAnimes(id int) ([]XinyuuStaffWork, error) {
	var raw struct {
		Code int               `json:"code"`
		Data []XinyuuStaffWork `json:"data"`
	}
	if err := xinyuuGet("/staff/"+strconv.Itoa(id)+"/animes", &raw); err != nil {
		return nil, err
	}
	if len(raw.Data) == 0 {
		return raw.Data, nil
	}
	type item struct {
		idx  int
		work XinyuuStaffWork
	}
	res := make(chan item, len(raw.Data))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i := range raw.Data {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			work := raw.Data[i]
			work.URL = "https://db.xinyuu.cn/"
			// Chinese metadata from Xinyuu search (cached)
			if v, ok := xyMeCache.Load(work.AnimeID); ok {
				applyXYMeta(&work, v.(XinyuuAnime))
			} else if m, ok := xySearchAnimeByTitle(work.TitleChinese, work.AnimeID); ok {
				xyMeCache.Store(work.AnimeID, m)
				applyXYMeta(&work, m)
			} else if m, ok := xySearchAnimeByTitle(work.TitleOriginal, work.AnimeID); ok {
				xyMeCache.Store(work.AnimeID, m)
				applyXYMeta(&work, m)
			} else {
				xyMeCache.Store(work.AnimeID, XinyuuAnime{AnimeID: work.AnimeID})
			}
			// Reachable cover (cached)
			if c, ok := xyCoverCache.Load(work.AnimeID); ok {
				wc := c.(workCover)
				work.CoverImage = wc.cover
				work.BgmID = wc.bgmID
			} else {
				wc := reachableCover(work.TitleChinese, work.TitleOriginal)
				xyCoverCache.Store(work.AnimeID, wc)
				work.CoverImage = wc.cover
				work.BgmID = wc.bgmID
			}
			res <- item{i, work}
		}(i)
	}
	go func() { wg.Wait(); close(res) }()
	out := make([]XinyuuStaffWork, len(raw.Data))
	for r := range res {
		out[r.idx] = r.work
	}
	return out, nil
}
