package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"nexusvalet/pkg/logger"
	"sync"

	_ "modernc.org/sqlite"
)

// Session represents a user session
type Session struct {
	UserID    int64                  `json:"user_id"`
	ChatID    int64                  `json:"chat_id"`
	Context   map[string]interface{} `json:"context"`
	Timestamp int64                  `json:"timestamp"`
}

// Manager manages user sessions
type Manager struct {
	db    *sql.DB
	mutex sync.RWMutex
}

// NewManager creates a new session manager
func NewManager(dbPath string) (*Manager, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	manager := &Manager{
		db: db,
	}

	if err := manager.initDatabase(); err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	logger.Debugf("Session manager initialized with database: %s", dbPath)
	return manager, nil
}

// initDatabase creates the sessions table if it doesn't exist
func (m *Manager) initDatabase() error {
	query := `
	CREATE TABLE IF NOT EXISTS sessions (
		user_id INTEGER,
		chat_id INTEGER,
		context TEXT,
		timestamp INTEGER,
		PRIMARY KEY (user_id, chat_id)
	)`

	_, err := m.db.Exec(query)
	return err
}

// GetSession retrieves a session for the given user and chat
func (m *Manager) GetSession(userID, chatID int64) (*Session, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	query := "SELECT user_id, chat_id, context, timestamp FROM sessions WHERE user_id = ? AND chat_id = ?"
	row := m.db.QueryRow(query, userID, chatID)

	var session Session
	var contextJSON string
	err := row.Scan(&session.UserID, &session.ChatID, &contextJSON, &session.Timestamp)
	if err != nil {
		if err == sql.ErrNoRows {
			// Create new session
			session = Session{
				UserID:  userID,
				ChatID:  chatID,
				Context: make(map[string]interface{}),
			}
			return &session, nil
		}
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	if err := json.Unmarshal([]byte(contextJSON), &session.Context); err != nil {
		return nil, fmt.Errorf("failed to unmarshal context: %w", err)
	}

	return &session, nil
}

// SaveSession saves a session to the database
func (m *Manager) SaveSession(session *Session) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	contextJSON, err := json.Marshal(session.Context)
	if err != nil {
		return fmt.Errorf("failed to marshal context: %w", err)
	}

	query := `
	INSERT OR REPLACE INTO sessions (user_id, chat_id, context, timestamp)
	VALUES (?, ?, ?, ?)`

	_, err = m.db.Exec(query, session.UserID, session.ChatID, string(contextJSON), session.Timestamp)
	if err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}

	return nil
}

// DeleteSession deletes a session from the database
func (m *Manager) DeleteSession(userID, chatID int64) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	query := "DELETE FROM sessions WHERE user_id = ? AND chat_id = ?"
	_, err := m.db.Exec(query, userID, chatID)
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	return nil
}

// Close closes the database connection
func (m *Manager) Close() error {
	return m.db.Close()
}

// SessionContext provides an interface for plugins to interact with session data
type SessionContext struct {
	session *Session
	manager *Manager
}

// NewSessionContext creates a new session context
func NewSessionContext(session *Session, manager *Manager) *SessionContext {
	return &SessionContext{
		session: session,
		manager: manager,
	}
}

// Get retrieves a value from the session context
func (sc *SessionContext) Get(key string) (interface{}, bool) {
	value, exists := sc.session.Context[key]
	return value, exists
}

// Set sets a value in the session context
func (sc *SessionContext) Set(key string, value interface{}) {
	if sc.session.Context == nil {
		sc.session.Context = make(map[string]interface{})
	}
	sc.session.Context[key] = value
}

// Delete removes a value from the session context
func (sc *SessionContext) Delete(key string) {
	delete(sc.session.Context, key)
}

// Save persists the session to the database
func (sc *SessionContext) Save() error {
	return sc.manager.SaveSession(sc.session)
}

// UserID returns the user ID
func (sc *SessionContext) UserID() int64 {
	return sc.session.UserID
}

// ChatID returns the chat ID
func (sc *SessionContext) ChatID() int64 {
	return sc.session.ChatID
}
