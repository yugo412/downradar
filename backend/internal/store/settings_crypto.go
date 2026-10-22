package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// sealedSettings is the envelope the settings column holds once it is encrypted.
// A plaintext settings object has neither of these fields.
type sealedSettings struct {
	Version int    `json:"v"`
	Data    string `json:"data"`
}

func sealedEnvelope(raw []byte) (sealedSettings, bool) {
	var envelope sealedSettings
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return envelope, false
	}
	if envelope.Version == 0 && envelope.Data == "" {
		return envelope, false
	}
	return envelope, true
}

// sealSettings encrypts the JSON a channel's settings column holds.
func (s *Store) sealSettings(plain []byte) ([]byte, error) {
	if s.secrets == nil {
		return nil, errors.New("store: the app key is not configured")
	}
	data, err := s.secrets.Seal(plain)
	if err != nil {
		return nil, err
	}
	return json.Marshal(sealedSettings{Version: 1, Data: data})
}

// openSettings decrypts a settings value, or returns a legacy plaintext one as
// it is. A value sealed with another key fails rather than reading as empty.
func (s *Store) openSettings(raw []byte) ([]byte, error) {
	envelope, sealed := sealedEnvelope(raw)
	if !sealed {
		return raw, nil
	}
	if envelope.Version != 1 {
		return nil, fmt.Errorf("store: unsupported settings version %d", envelope.Version)
	}
	if s.secrets == nil {
		return nil, errors.New("store: the app key is not configured")
	}
	return s.secrets.Open(envelope.Data)
}

func (s *Store) parseWebhookSettings(raw []byte, address string) (AlertWebhook, error) {
	plain, err := s.openSettings(raw)
	if err != nil {
		return AlertWebhook{}, err
	}
	return parseWebhookSettings(plain, address)
}

func (s *Store) parseTelegramSettings(raw []byte) (string, error) {
	plain, err := s.openSettings(raw)
	if err != nil {
		return "", err
	}
	return parseTelegramSettings(plain)
}

func (s *Store) parseSlackSettings(raw []byte) (string, error) {
	plain, err := s.openSettings(raw)
	if err != nil {
		return "", err
	}
	return parseSlackSettings(plain)
}

// SealLegacySettings encrypts settings written before the key existed, and
// checks that values already sealed can be opened with the configured key. It
// runs at startup, so a wrong key stops the install before a delivery can send
// an empty header list.
func (s *Store) SealLegacySettings(ctx context.Context) error {
	return s.sealLegacySettings(ctx, "")
}

// sealLegacySettings is the same pass with an optional row filter, which is what
// a test uses so it never touches the rows of another test in the shared database.
func (s *Store) sealLegacySettings(ctx context.Context, where string, args ...any) error {
	if s.secrets == nil {
		return errors.New("store: the app key is not configured")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin settings migration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	query := `select id, settings from alert_channels where settings <> '{}'::jsonb`
	if where != "" {
		query += " and " + where
	}
	query += " order by id"

	type row struct {
		id  int64
		raw []byte
	}
	var rows []row
	selected, err := tx.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store: read channel settings: %w", err)
	}
	for selected.Next() {
		var r row
		if err := selected.Scan(&r.id, &r.raw); err != nil {
			selected.Close()
			return fmt.Errorf("store: scan channel settings: %w", err)
		}
		rows = append(rows, r)
	}
	selected.Close()
	if err := selected.Err(); err != nil {
		return fmt.Errorf("store: read channel settings: %w", err)
	}

	for _, r := range rows {
		if envelope, sealed := sealedEnvelope(r.raw); sealed {
			if envelope.Version != 1 {
				return fmt.Errorf("store: channel %d has settings version %d", r.id, envelope.Version)
			}
			if _, err := s.secrets.Open(envelope.Data); err != nil {
				return fmt.Errorf("store: channel %d cannot be opened: %w", r.id, err)
			}
			continue
		}

		sealed, err := s.sealSettings(r.raw)
		if err != nil {
			return fmt.Errorf("store: seal channel %d settings: %w", r.id, err)
		}
		if _, err := tx.Exec(ctx, `
			update alert_channels set settings = $2, updated_at = now()
			where id = $1`, r.id, sealed); err != nil {
			return fmt.Errorf("store: save channel %d settings: %w", r.id, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit settings migration: %w", err)
	}
	return nil
}
