package ledger

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestFileAndRunAttachmentStore(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "files.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}

	now := time.Now().UTC()
	if err := store.CreateFile(context.Background(), FileRecord{
		FileID:       "f1",
		StorageKey:   "f1.bin",
		OriginalName: "spec.md",
		MIMEType:     "text/markdown",
		SizeBytes:    123,
		SHA256:       "abc",
		CreatedBy:    "test",
		CreatedAt:    now,
	}); err != nil {
		t.Fatalf("create file: %v", err)
	}
	got, err := store.GetFile(context.Background(), "f1")
	if err != nil {
		t.Fatalf("get file: %v", err)
	}
	if got.OriginalName != "spec.md" || got.SizeBytes != 123 {
		t.Fatalf("unexpected file row: %#v", got)
	}

	if err := store.CreateRunAttachment(context.Background(), RunAttachmentRecord{
		RunID:            "r1",
		FileID:           "f1",
		Alias:            "spec.md",
		MaterializedPath: ".elix/attachments/spec.md",
		CreatedAt:        now,
	}); err != nil {
		t.Fatalf("create run attachment: %v", err)
	}
	attachments, err := store.ListRunAttachments(context.Background(), "r1")
	if err != nil {
		t.Fatalf("list run attachments: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if attachments[0].FileID != "f1" || attachments[0].Alias != "spec.md" {
		t.Fatalf("unexpected attachment row: %#v", attachments[0])
	}
}
