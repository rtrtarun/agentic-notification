package event

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type SQLExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type PostgresRepository struct {
	queryer SQLExecutor
}

func NewPostgresRepository(queryer SQLExecutor) *PostgresRepository {
	return &PostgresRepository{queryer: queryer}
}

func (r *PostgresRepository) Save(ctx context.Context, evt Event) error {
	payload, err := json.Marshal(evt.Payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	var decisionStatus, decisionReason, decisionModelVersion, decisionMatchedRule sql.NullString
	var decisionConfidence sql.NullFloat64

	if evt.DecisionState != nil {
		decisionStatus = sql.NullString{String: evt.DecisionState.Status, Valid: evt.DecisionState.Status != ""}
		decisionReason = sql.NullString{String: evt.DecisionState.Reason, Valid: evt.DecisionState.Reason != ""}
		decisionModelVersion = sql.NullString{String: evt.DecisionState.ModelVersion, Valid: evt.DecisionState.ModelVersion != ""}
		decisionMatchedRule = sql.NullString{String: evt.DecisionState.MatchedRule, Valid: evt.DecisionState.MatchedRule != ""}
		if evt.DecisionState.Confidence > 0 {
			decisionConfidence = sql.NullFloat64{Float64: evt.DecisionState.Confidence, Valid: true}
		}
	}

	attentionClass := evt.AttentionClass
	if attentionClass == "" {
		attentionClass = AttentionStandard
	}

	_, err = r.queryer.ExecContext(
		ctx,
		`INSERT INTO events (
			id, source, event_type, repository,
			attention_class, decision_status, decision_reason,
			decision_model_version, decision_confidence, decision_matched_rule,
			payload, dedup_key, thread_id, merged_into
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (id) DO NOTHING`,
		evt.ID,
		evt.Source,
		evt.EventType,
		evt.Repository,
		attentionClass,
		decisionStatus,
		decisionReason,
		decisionModelVersion,
		decisionConfidence,
		decisionMatchedRule,
		string(payload),
		nullString(evt.DedupKey),
		nullString(evt.ThreadID),
		nullString(evt.MergedInto),
	)
	return err
}

func (r *PostgresRepository) List(ctx context.Context) ([]Event, error) {
	rows, err := r.queryer.QueryContext(ctx, `
		SELECT id, source, event_type, repository,
		       attention_class, decision_status, decision_reason,
		       decision_model_version, decision_confidence, decision_matched_rule,
		       payload, dedup_key, thread_id, merged_into, created_at
		FROM events ORDER BY created_at DESC LIMIT $1`, DefaultListLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Event
	for rows.Next() {
		var evt Event
		var payload string
		var decisionStatus, decisionReason, decisionModelVersion, decisionMatchedRule sql.NullString
		var decisionConfidence sql.NullFloat64
		var repository, dedupKey, threadID, mergedInto sql.NullString

		if err := rows.Scan(
			&evt.ID, &evt.Source, &evt.EventType, &repository,
			&evt.AttentionClass, &decisionStatus, &decisionReason,
			&decisionModelVersion, &decisionConfidence, &decisionMatchedRule,
			&payload, &dedupKey, &threadID, &mergedInto, &evt.CreatedAt,
		); err != nil {
			return nil, err
		}

		if err := json.Unmarshal([]byte(payload), &evt.Payload); err != nil {
			return nil, err
		}

		evt.Repository = repository.String
		evt.DedupKey, evt.ThreadID, evt.MergedInto = dedupKey.String, threadID.String, mergedInto.String

		if decisionStatus.Valid || decisionReason.Valid {
			evt.DecisionState = &DecisionState{
				Status:       decisionStatus.String,
				Reason:       decisionReason.String,
				ModelVersion: decisionModelVersion.String,
				MatchedRule:  decisionMatchedRule.String,
			}
			if decisionConfidence.Valid {
				evt.DecisionState.Confidence = decisionConfidence.Float64
			}
		}

		results = append(results, evt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	// The query returns newest first so LIMIT keeps the most recent rows.
	for i, j := 0, len(results)-1; i < j; i, j = i+1, j-1 {
		results[i], results[j] = results[j], results[i]
	}
	return results, nil
}

// Exists reports whether an event with this ID is already stored.
func (r *PostgresRepository) Exists(ctx context.Context, id string) (bool, error) {
	rows, err := r.queryer.QueryContext(ctx, `SELECT 1 FROM events WHERE id = $1`, id)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	found := rows.Next()
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("check event exists: %w", err)
	}
	return found, nil
}

// DeleteOlderThan removes events created before cutoff and returns how many.
func (r *PostgresRepository) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := r.queryer.ExecContext(ctx, `DELETE FROM events WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	if res == nil {
		return 0, nil
	}
	return res.RowsAffected()
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
