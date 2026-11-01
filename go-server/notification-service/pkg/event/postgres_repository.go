package event

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

type SQLExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

type PostgresRepository struct {
	queryer SQLExecutor
}

func NewPostgresRepository(queryer SQLExecutor) *PostgresRepository {
	return &PostgresRepository{queryer: queryer}
}

func (r *PostgresRepository) Save(evt Event) error {
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

	_, err = r.queryer.Exec(
		`INSERT INTO events (
			id, source, event_type, repository,
			attention_class, decision_status, decision_reason,
			decision_model_version, decision_confidence, decision_matched_rule,
			payload, dedup_key, thread_id, merged_into
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
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

func (r *PostgresRepository) List() ([]Event, error) {
	rows, err := r.queryer.Query(`
		SELECT id, source, event_type, repository,
		       attention_class, decision_status, decision_reason,
		       decision_model_version, decision_confidence, decision_matched_rule,
		       payload, dedup_key, thread_id, merged_into
		FROM events ORDER BY created_at`)
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
		var dedupKey, threadID, mergedInto sql.NullString

		if err := rows.Scan(
			&evt.ID, &evt.Source, &evt.EventType, &evt.Repository,
			&evt.AttentionClass, &decisionStatus, &decisionReason,
			&decisionModelVersion, &decisionConfidence, &decisionMatchedRule,
			&payload, &dedupKey, &threadID, &mergedInto,
		); err != nil {
			return nil, err
		}

		if err := json.Unmarshal([]byte(payload), &evt.Payload); err != nil {
			return nil, err
		}

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
	return results, nil
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
