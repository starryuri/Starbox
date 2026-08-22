package githot

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Repo is one GitHub repository.
type Repo struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Stars  int    `json:"stars"`
	Desc   string `json:"desc"`
	Lang   string `json:"lang"`
	Avatar string `json:"avatar"`
}

// Account is the authenticated GitHub user.
type Account struct {
	Login  string `json:"login"`
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
	URL    string `json:"url"`
}

// Trending returns the most-starred repos created within the last `days` days.
func Trending(days int, token string) ([]Repo, error) {
	if days <= 0 {
		days = 7
	}
	since := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
	u := "https://api.github.com/search/repositories?q=created:>" + since +
		"&sort=stars&order=desc&per_page=12"
	var raw struct {
		Items []struct {
			FullName        string `json:"full_name"`
			HTMLURL         string `json:"html_url"`
			Description     string `json:"description"`
			Language        string `json:"language"`
			StargazersCount int    `json:"stargazers_count"`
			Owner           struct {
				AvatarURL string `json:"avatar_url"`
			} `json:"owner"`
		} `json:"items"`
	}
	if err := get(u, token, &raw); err != nil {
		return nil, err
	}
	out := make([]Repo, 0, len(raw.Items))
	for _, it := range raw.Items {
		out = append(out, Repo{Name: it.FullName, URL: it.HTMLURL, Stars: it.StargazersCount, Desc: it.Description, Lang: it.Language, Avatar: it.Owner.AvatarURL})
	}
	return out, nil
}

// MyRepos returns the authenticated user's most-recently-updated repos.
func MyRepos(token string) ([]Repo, error) {
	var items []struct {
		FullName        string `json:"full_name"`
		HTMLURL         string `json:"html_url"`
		Description     string `json:"description"`
		Language        string `json:"language"`
		StargazersCount int    `json:"stargazers_count"`
		Owner           struct {
			AvatarURL string `json:"avatar_url"`
		} `json:"owner"`
	}
	if err := get("https://api.github.com/user/repos?sort=updated&per_page=10", token, &items); err != nil {
		return nil, err
	}
	out := make([]Repo, 0, len(items))
	for _, it := range items {
		out = append(out, Repo{Name: it.FullName, URL: it.HTMLURL, Stars: it.StargazersCount, Desc: it.Description, Lang: it.Language, Avatar: it.Owner.AvatarURL})
	}
	return out, nil
}

// Auth verifies a personal access token against GitHub and returns the account.
func Auth(token string) (Account, error) {
	var raw struct {
		Login  string `json:"login"`
		Name   string `json:"name"`
		HTML   string `json:"html_url"`
		Avatar string `json:"avatar_url"`
	}
	if err := get("https://api.github.com/user", token, &raw); err != nil {
		return Account{}, err
	}
	return Account{Login: raw.Login, Name: raw.Name, Avatar: raw.Avatar, URL: raw.HTML}, nil
}

// get performs a GET to GitHub's API, optionally with a bearer token, and
// decodes the JSON into out.
func get(url, token string, out interface{}) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "starbox/0.1")
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return fmt.Errorf("无效令牌")
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("bad status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return json.Unmarshal(body, out)
}
