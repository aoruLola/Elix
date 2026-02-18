package run

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestUploadAndGetFile(t *testing.T) {
	svc := setupService(t, newFakeDriver("codex", false))
	svc.SetFileStorage(filepath.Join(t.TempDir(), "files"), 1024)

	uploaded, err := svc.UploadFile(context.Background(), UploadFileRequest{
		Reader:       bytes.NewReader([]byte("hello file")),
		OriginalName: "spec.md",
		MIMEType:     "text/markdown",
		CreatedBy:    "test",
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if uploaded.FileID == "" || uploaded.SizeBytes == 0 {
		t.Fatalf("unexpected upload result: %#v", uploaded)
	}

	got, err := svc.GetUploadedFile(context.Background(), uploaded.FileID)
	if err != nil {
		t.Fatalf("get uploaded file: %v", err)
	}
	if got.FileID != uploaded.FileID || got.OriginalName != "spec.md" {
		t.Fatalf("unexpected file metadata: %#v", got)
	}
}

func TestUploadFileTooLarge(t *testing.T) {
	svc := setupService(t, newFakeDriver("codex", false))
	svc.SetFileStorage(filepath.Join(t.TempDir(), "files"), 4)

	_, err := svc.UploadFile(context.Background(), UploadFileRequest{
		Reader:       bytes.NewReader([]byte("1234567")),
		OriginalName: "big.txt",
		MIMEType:     "text/plain",
		CreatedBy:    "test",
	})
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("expected ErrFileTooLarge, got %v", err)
	}
}
