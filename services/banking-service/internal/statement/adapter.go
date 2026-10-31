package statement

import (
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"strconv"
	"sync"
	"time"
)

// Adapter is the read-only boundary for future official bank connectors.
// Authorization acquisition belongs to a provider's official consent flow.
// Password collection and financial execution are deliberately absent.
type Adapter interface {
	Status(context.Context, string) (Connection, error)
	ReadTransactions(context.Context, string, int, int) (SyncPage, error)
	Revoke(context.Context, string) error
}
type Connection struct {
	ID           string   `json:"id"`
	UserID       string   `json:"user_id"`
	Provider     string   `json:"provider"`
	State        string   `json:"state"`
	Scopes       []string `json:"scopes"`
	IsSimulated  bool     `json:"is_simulated"`
	LastSyncedAt int64    `json:"last_synced_at"`
	RevokedAt    int64    `json:"revoked_at,omitempty"`
}
type SyncPage struct {
	Connection Connection `json:"connection"`
	Records    []Record   `json:"records"`
	Total      int        `json:"total"`
}

// SimulationAdapter is an explicitly constructed, process-local adapter for
// contract tests. It is not a bank connection or a persisted consent store.
type SimulationAdapter struct {
	mu         sync.Mutex
	connection Connection
	records    []Record
}

func NewSimulationAdapter(user string, in Input) (*SimulationAdapter, error) {
	id, err := strconv.ParseInt(user, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != user {
		return nil, invalid()
	}
	preview, err := Parse(in)
	if err != nil {
		return nil, err
	}
	return &SimulationAdapter{connection: Connection{ID: "simulation:" + user + ":" + preview.FileSHA256, UserID: user, Provider: "local_simulation", State: "simulated", Scopes: []string{"transactions:read"}, IsSimulated: true}, records: preview.Records}, nil
}
func (s *SimulationAdapter) owned(ctx context.Context, user string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if user != s.connection.UserID {
		return entity.Fail("NOT_FOUND", "owned adapter connection not found")
	}
	return nil
}
func (s *SimulationAdapter) Status(ctx context.Context, user string) (Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.owned(ctx, user); err != nil {
		return Connection{}, err
	}
	state := s.connection
	state.Scopes = append([]string{}, state.Scopes...)
	return state, nil
}
func (s *SimulationAdapter) ReadTransactions(ctx context.Context, user string, offset, limit int) (SyncPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.owned(ctx, user); err != nil {
		return SyncPage{}, err
	}
	if s.connection.State == "revoked" {
		return SyncPage{}, entity.Fail("FORBIDDEN", "adapter read consent revoked")
	}
	if offset < 0 || offset > MaxRecords || limit < 1 || limit > 100 {
		return SyncPage{}, invalid()
	}
	start := min(offset, len(s.records))
	end := min(start+limit, len(s.records))
	records := []Record{}
	raw, _ := json.Marshal(s.records[start:end])
	if err := json.Unmarshal(raw, &records); err != nil {
		return SyncPage{}, err
	}
	s.connection.LastSyncedAt = time.Now().UnixMilli()
	state := s.connection
	state.Scopes = append([]string{}, state.Scopes...)
	return SyncPage{Connection: state, Records: records, Total: len(s.records)}, nil
}
func (s *SimulationAdapter) Revoke(ctx context.Context, user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.owned(ctx, user); err != nil {
		return err
	}
	if s.connection.State != "revoked" {
		s.connection.State = "revoked"
		s.connection.Scopes = []string{}
		s.connection.RevokedAt = time.Now().UnixMilli()
	}
	return nil
}
