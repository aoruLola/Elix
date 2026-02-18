package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"echohelix/internal/ledger"

	"github.com/google/uuid"
)

var (
	ErrFileTooLarge = errors.New("uploaded file exceeds max size")
	ErrFileNotFound = errors.New("file not found")
)

type UploadFileRequest struct {
	Reader       io.Reader
	OriginalName string
	MIMEType     string
	CreatedBy    string
}

type UploadedFile struct {
	FileID       string    `json:"file_id"`
	OriginalName string    `json:"original_name"`
	MIMEType     string    `json:"mime_type"`
	SizeBytes    int64     `json:"size_bytes"`
	SHA256       string    `json:"sha256"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
}

func (s *Service) SetFileStorage(dir string, maxUploadBytes int64) {
	dir = strings.TrimSpace(dir)
	if dir != "" {
		s.fileStoreDir = dir
	}
	if maxUploadBytes > 0 {
		s.maxUploadBytes = maxUploadBytes
	}
}

func (s *Service) MaxUploadBytes() int64 {
	if s.maxUploadBytes <= 0 {
		return 20 * 1024 * 1024
	}
	return s.maxUploadBytes
}

func (s *Service) UploadFile(ctx context.Context, req UploadFileRequest) (UploadedFile, error) {
	if req.Reader == nil {
		return UploadedFile{}, fmt.Errorf("file stream is required")
	}
	name := strings.TrimSpace(req.OriginalName)
	if name == "" {
		name = "upload.bin"
	}
	if err := os.MkdirAll(s.fileStoreDir, 0o750); err != nil {
		return UploadedFile{}, fmt.Errorf("prepare file store: %w", err)
	}

	fileID := uuid.NewString()
	storageKey := fileID + ".bin"
	targetPath := filepath.Join(s.fileStoreDir, storageKey)

	tmp, err := os.CreateTemp(s.fileStoreDir, "upload-*")
	if err != nil {
		return UploadedFile{}, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	hash := sha256.New()
	limit := s.MaxUploadBytes()
	lr := &io.LimitedReader{R: req.Reader, N: limit + 1}
	n, copyErr := io.Copy(io.MultiWriter(tmp, hash), lr)
	if closeErr := tmp.Close(); closeErr != nil && copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return UploadedFile{}, copyErr
	}
	if n > limit {
		return UploadedFile{}, ErrFileTooLarge
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return UploadedFile{}, err
	}

	now := time.Now().UTC()
	rec := ledger.FileRecord{
		FileID:       fileID,
		StorageKey:   storageKey,
		OriginalName: name,
		MIMEType:     strings.TrimSpace(req.MIMEType),
		SizeBytes:    n,
		SHA256:       hex.EncodeToString(hash.Sum(nil)),
		CreatedBy:    strings.TrimSpace(req.CreatedBy),
		CreatedAt:    now,
	}
	if err := s.ledger.CreateFile(ctx, rec); err != nil {
		_ = os.Remove(targetPath)
		return UploadedFile{}, err
	}
	return UploadedFile{
		FileID:       rec.FileID,
		OriginalName: rec.OriginalName,
		MIMEType:     rec.MIMEType,
		SizeBytes:    rec.SizeBytes,
		SHA256:       rec.SHA256,
		CreatedBy:    rec.CreatedBy,
		CreatedAt:    rec.CreatedAt,
	}, nil
}

func (s *Service) GetUploadedFile(ctx context.Context, fileID string) (UploadedFile, error) {
	rec, err := s.ledger.GetFile(ctx, strings.TrimSpace(fileID))
	if err != nil {
		if errors.Is(err, ledger.ErrFileNotFound) {
			return UploadedFile{}, ErrFileNotFound
		}
		return UploadedFile{}, err
	}
	return UploadedFile{
		FileID:       rec.FileID,
		OriginalName: rec.OriginalName,
		MIMEType:     rec.MIMEType,
		SizeBytes:    rec.SizeBytes,
		SHA256:       rec.SHA256,
		CreatedBy:    rec.CreatedBy,
		CreatedAt:    rec.CreatedAt,
	}, nil
}
