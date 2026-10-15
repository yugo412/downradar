package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"downradar.info/backend/internal/adapters"
)

func (s *Store) guardPageIdentity(ctx context.Context, tx pgx.Tx, serviceID int64, pageID string) error {
	if pageID == "" {
		return nil
	}

	var slug, pinned string
	var allowShared bool
	if err := tx.QueryRow(ctx, `
		select slug, coalesce(page_id, ''), allow_shared_page
		from services
		where id = $1
		for update`, serviceID).Scan(&slug, &pinned, &allowShared); err != nil {
		return fmt.Errorf("apply: read page identity: %w", err)
	}

	if pinned != "" && pinned != pageID {
		return fmt.Errorf(
			"apply: %s was pinned to status page %s but now reports %s, refusing to write",
			slug, pinned, pageID)
	}

	var otherSlug string
	var otherAllowShared bool
	err := tx.QueryRow(ctx, `
		select slug, allow_shared_page
		from services
		where active and id <> $1 and page_id = $2
		limit 1`, serviceID, pageID).Scan(&otherSlug, &otherAllowShared)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return fmt.Errorf("apply: look for page collisions: %w", err)
	case !allowShared || !otherAllowShared:
		return fmt.Errorf(
			"apply: %s reports the same status page (%s) as %s, refusing to write; set shared_page: true on both vendors if that is intended",
			slug, pageID, otherSlug)
	}

	if pinned == "" {
		if _, err := tx.Exec(ctx,
			`update services set page_id = $2, updated_at = now() where id = $1`,
			serviceID, pageID); err != nil {
			return fmt.Errorf("apply: pin page id: %w", err)
		}
	}
	return nil
}

func (s *Store) storeRawPayload(ctx context.Context, tx pgx.Tx, serviceID int64, fetchedAt time.Time, rep adapters.Report) error {
	if len(rep.Raw) == 0 {
		return nil
	}

	var previous string
	var previousAt time.Time
	err := tx.QueryRow(ctx, `
		select body_hash, fetched_at from raw_payloads
		where service_id = $1
		order by fetched_at desc
		limit 1`, serviceID).Scan(&previous, &previousAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return fmt.Errorf("apply: read the previous payload: %w", err)
	case fetchedAt.Sub(previousAt) < s.rawPayloadMinInterval:
		return nil
	}

	sum := sha256.Sum256(rep.Raw)
	hash := hex.EncodeToString(sum[:])
	if previous == hash {
		return nil
	}

	compressed, err := gzipBytes(rep.Raw)
	if err != nil {
		return fmt.Errorf("apply: compress raw payload: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		insert into raw_payloads (service_id, fetched_at, http_status, body, body_hash)
		values ($1, $2, $3, $4, $5)`,
		serviceID, fetchedAt, nullInt(rep.HTTPStatus), compressed, hash); err != nil {
		return fmt.Errorf("apply: store raw payload: %w", err)
	}
	return nil
}

func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Store) DeleteRawPayloadsOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		delete from raw_payloads
		where fetched_at < now() - make_interval(secs => $1)`,
		age.Seconds())
	if err != nil {
		return 0, fmt.Errorf("raw payloads: delete old rows: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (s *Store) RawPayloadStats(ctx context.Context) (rows int64, bytes int64, err error) {
	err = s.pool.QueryRow(ctx, `
		select count(*), coalesce(sum(length(body)), 0)
		from raw_payloads`).Scan(&rows, &bytes)
	if err != nil {
		return 0, 0, fmt.Errorf("raw payloads: stats: %w", err)
	}
	return rows, bytes, nil
}
