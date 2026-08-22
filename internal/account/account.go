package account

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// User is one local account. The password is stored as a salted SHA-256 hash,
// never in plaintext. Theme is a per-user personalization preference.
type User struct {
	ID       string                 `json:"id"`
	Nickname string                 `json:"nickname"`
	Salt     string                 `json:"salt"`
	Hash     string                 `json:"hash"`
	Theme    string                 `json:"theme"`
	Created  int64                  `json:"created"`
	Data     map[string]interface{} `json:"data,omitempty"`
}

// Manager stores local users + sessions in the app's data directory.
type Manager struct {
	mu       sync.Mutex
	dir      string
	users    map[string]User   // id -> user
	sessions map[string]string // token -> user id
}

func hashPass(salt, pass string) string {
	sum := sha256.Sum256([]byte(salt + pass))
	return hex.EncodeToString(sum[:])
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// New loads (or creates) the account + session stores under dir.
func New(dir string) (*Manager, error) {
	_ = os.MkdirAll(dir, 0o755)
	m := &Manager{dir: dir, users: map[string]User{}, sessions: map[string]string{}}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) usersFile() string    { return filepath.Join(m.dir, "accounts.json") }
func (m *Manager) sessionFile() string  { return filepath.Join(m.dir, "session.json") }

func (m *Manager) load() error {
	if b, err := os.ReadFile(m.usersFile()); err == nil {
		_ = json.Unmarshal(b, &m.users)
	}
	if b, err := os.ReadFile(m.sessionFile()); err == nil {
		_ = json.Unmarshal(b, &m.sessions)
	}
	return nil
}

func (m *Manager) saveUsers() error {
	b, _ := json.MarshalIndent(m.users, "", "  ")
	return os.WriteFile(m.usersFile(), b, 0o644)
}

func (m *Manager) saveSessions() error {
	b, _ := json.MarshalIndent(m.sessions, "", "  ")
	return os.WriteFile(m.sessionFile(), b, 0o644)
}

// HasUsers reports whether any account exists yet (drives register vs login UI).
func (m *Manager) HasUsers() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.users) > 0
}

// UserIDs returns the ids of all registered accounts (for system-wide writers).
func (m *Manager) UserIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.users))
	for id := range m.users {
		ids = append(ids, id)
	}
	return ids
}

// Register creates a new account and returns the user + a fresh session token.
func (m *Manager) Register(nickname, password string) (User, string, error) {
	if nickname == "" || password == "" {
		return User{}, "", errors.New("昵称和密码不能为空")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.Nickname == nickname {
			return User{}, "", errors.New("该昵称已被注册")
		}
	}
	salt := randHex(8)
	u := User{
		ID:       randHex(8),
		Nickname: nickname,
		Salt:     salt,
		Hash:     hashPass(salt, password),
		Theme:    "dark",
		Created:  time.Now().Unix(),
		Data:     map[string]interface{}{},
	}
	m.users[u.ID] = u
	if err := m.saveUsers(); err != nil {
		return User{}, "", err
	}
	tok := m.createSession(u.ID)
	return u, tok, nil
}

// Login verifies credentials and returns the user + a fresh session token.
func (m *Manager) Login(nickname, password string) (User, string, error) {
	if nickname == "" || password == "" {
		return User{}, "", errors.New("昵称和密码不能为空")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.Nickname == nickname {
			if u.Hash == hashPass(u.Salt, password) {
				tok := m.createSession(u.ID)
				return u, tok, nil
			}
			return User{}, "", errors.New("密码不正确")
		}
	}
	return User{}, "", errors.New("该昵称尚未注册")
}

func (m *Manager) createSession(uid string) string {
	tok := randHex(16)
	m.sessions[tok] = uid
	_ = m.saveSessions()
	return tok
}

// Logout invalidates a session token.
func (m *Manager) Logout(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if token != "" {
		delete(m.sessions, token)
		_ = m.saveSessions()
	}
}

// Session resolves a token to its user (if any).
func (m *Manager) Session(token string) (User, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	uid, ok := m.sessions[token]
	if !ok {
		return User{}, false
	}
	u, ok := m.users[uid]
	return u, ok
}

// SetTheme persists a per-user theme preference.
func (m *Manager) SetTheme(uid, theme string) error {
	if theme != "dark" && theme != "light" {
		theme = "dark"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[uid]
	if !ok {
		return fmt.Errorf("user not found")
	}
	u.Theme = theme
	m.users[uid] = u
	return m.saveUsers()
}
