package store

import (
	"context"
	"fmt"
	"time"
)

// SaveGeneratedSummary replaces the one summary a vendor may have. The input
// hash is stored with it so a later pass can tell whether the state it describes
// is still the state the page shows.
func (s *Store) SaveGeneratedSummary(ctx context.Context, serviceID int64, text, model, inputHash string, generatedAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		insert into service_summaries
			(service_id, summary, model, input_hash, generated_at, updated_at)
		values ($1, $2, $3, $4, $5, now())
		on conflict (service_id) do update set
			summary      = excluded.summary,
			model        = excluded.model,
			input_hash   = excluded.input_hash,
			generated_at = excluded.generated_at,
			updated_at   = now()`,
		serviceID, text, model, inputHash, generatedAt,
	)
	if err != nil {
		return fmt.Errorf("summaries: save for service %d: %w", serviceID, err)
	}
	return nil
}

// ClearGeneratedSummary removes a vendor's summary. A healthy vendor has nothing
// to summarise, and a changed state must not keep the sentence written for the
// old one.
func (s *Store) ClearGeneratedSummary(ctx context.Context, serviceID int64) error {
	if _, err := s.pool.Exec(ctx,
		`delete from service_summaries where service_id = $1`, serviceID,
	); err != nil {
		return fmt.Errorf("summaries: clear for service %d: %w", serviceID, err)
	}
	return nil
}
