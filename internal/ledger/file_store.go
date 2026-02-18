package ledger

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrFileNotFound = errors.New("file not found")

type FileRecord struct {
	FileID       string
	StorageKey   string
	OriginalName string
	MIMEType     string
	SizeBytes    int64
	SHA256       string
	CreatedBy    string
	CreatedAt    time.Time
}

type RunAttachmentRecord struct {
	RunID            string
	FileID           string
	Alias            string
	MaterializedPath string
	CreatedAt        time.Time
}

func (s *Store) CreateFile(ctx context.Context, rec FileRecord) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO files(file_id, storage_key, original_name, mime_type, size_bytes, sha256, created_by, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.FileID,
		rec.StorageKey,
		rec.OriginalName,
		rec.MIMEType,
		rec.SizeBytes,
		rec.SHA256,
		rec.CreatedBy,
		rec.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) GetFile(ctx context.Context, fileID string) (FileRecord, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT file_id, storage_key, original_name, mime_type, size_bytes, sha256, created_by, created_at
		 FROM files
		 WHERE file_id=?`,
		fileID,
	)
	var out FileRecord
	var ts string
	if err := row.Scan(
		&out.FileID,
		&out.StorageKey,
		&out.OriginalName,
		&out.MIMEType,
		&out.SizeBytes,
		&out.SHA256,
		&out.CreatedBy,
		&ts,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FileRecord{}, ErrFileNotFound
		}
		return FileRecord{}, err
	}
	out.CreatedAt, _ = time.Parse(time.RFC3339Nano, ts)
	return out, nil
}

func (s *Store) CreateRunAttachment(ctx context.Context, rec RunAttachmentRecord) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO run_attachments(run_id, file_id, alias, materialized_path, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(run_id, alias) DO UPDATE SET
		   file_id=excluded.file_id,
		   materialized_path=excluded.materialized_path,
		   created_at=excluded.created_at`,
		rec.RunID,
		rec.FileID,
		rec.Alias,
		rec.MaterializedPath,
		rec.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) ListRunAttachments(ctx context.Context, runID string) ([]RunAttachmentRecord, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT run_id, file_id, alias, materialized_path, created_at
		 FROM run_attachments
		 WHERE run_id=?
		 ORDER BY alias ASC`,
		runID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []RunAttachmentRecord{}
	for rows.Next() {
		var item RunAttachmentRecord
		var ts string
		if err := rows.Scan(&item.RunID, &item.FileID, &item.Alias, &item.MaterializedPath, &ts); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, item)
	}
	return out, rows.Err()
}
