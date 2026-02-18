package run

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"echohelix/internal/ledger"
)

var mentionAliasPattern = regexp.MustCompile(`@([A-Za-z0-9][A-Za-z0-9._-]{0,127})`)

type attachmentRef struct {
	FileID string
	Alias  string
}

func (s *Service) prepareAttachments(
	ctx context.Context,
	runID string,
	workspacePath string,
	prompt string,
	contextMap map[string]any,
) (string, map[string]any, []RunAttachment, error) {
	refs, err := parseAttachmentRefs(contextMap)
	if err != nil {
		return "", nil, nil, err
	}
	if len(refs) == 0 {
		return prompt, contextMap, nil, nil
	}
	absWorkspace, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", nil, nil, fmt.Errorf("resolve workspace path: %w", err)
	}
	attachRoot := filepath.Join(absWorkspace, ".elix", "attachments")
	if err := os.MkdirAll(attachRoot, 0o755); err != nil {
		return "", nil, nil, fmt.Errorf("prepare attachment dir: %w", err)
	}

	aliasToPath := map[string]string{}
	usedAlias := map[string]struct{}{}
	attachments := make([]RunAttachment, 0, len(refs))
	for _, ref := range refs {
		fileRec, err := s.ledger.GetFile(ctx, ref.FileID)
		if err != nil {
			return "", nil, nil, err
		}
		alias := chooseAlias(ref.Alias, fileRec.OriginalName, fileRec.FileID, usedAlias)
		usedAlias[alias] = struct{}{}

		relPath := filepath.ToSlash(filepath.Join(".elix", "attachments", alias))
		dst := filepath.Join(absWorkspace, filepath.FromSlash(relPath))
		if err := copyFile(filepath.Join(s.fileStoreDir, fileRec.StorageKey), dst); err != nil {
			return "", nil, nil, err
		}
		if err := s.ledger.CreateRunAttachment(ctx, ledger.RunAttachmentRecord{
			RunID:            runID,
			FileID:           fileRec.FileID,
			Alias:            alias,
			MaterializedPath: relPath,
			CreatedAt:        time.Now().UTC(),
		}); err != nil {
			return "", nil, nil, err
		}
		target := "./" + relPath
		aliasToPath[alias] = target
		attachments = append(attachments, RunAttachment{
			FileID:    fileRec.FileID,
			Alias:     alias,
			Path:      target,
			SizeBytes: fileRec.SizeBytes,
			SHA256:    fileRec.SHA256,
		})
	}

	rewrittenPrompt, mentionMap := rewritePromptMentions(prompt, aliasToPath)
	rewrittenPrompt = appendAttachmentHint(rewrittenPrompt, aliasToPath)

	if contextMap == nil {
		contextMap = map[string]any{}
	}
	resolved := make([]map[string]any, 0, len(attachments))
	for _, item := range attachments {
		resolved = append(resolved, map[string]any{
			"file_id":    item.FileID,
			"alias":      item.Alias,
			"path":       item.Path,
			"size_bytes": item.SizeBytes,
			"sha256":     item.SHA256,
		})
	}
	contextMap["resolved_attachments"] = resolved
	if len(mentionMap) > 0 {
		contextMap["mention_replacements"] = mentionMap
	}
	return rewrittenPrompt, contextMap, attachments, nil
}

func parseAttachmentRefs(contextMap map[string]any) ([]attachmentRef, error) {
	if contextMap == nil {
		return nil, nil
	}
	raw, ok := contextMap["attachments"]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("context.attachments must be an array")
	}
	out := make([]attachmentRef, 0, len(list))
	for i, item := range list {
		switch v := item.(type) {
		case string:
			fileID := strings.TrimSpace(v)
			if fileID == "" {
				return nil, fmt.Errorf("context.attachments[%d] file id is empty", i)
			}
			out = append(out, attachmentRef{FileID: fileID})
		case map[string]any:
			fileID := strings.TrimSpace(anyString(v["file_id"]))
			if fileID == "" {
				return nil, fmt.Errorf("context.attachments[%d].file_id is required", i)
			}
			alias := strings.TrimSpace(anyString(v["alias"]))
			out = append(out, attachmentRef{FileID: fileID, Alias: alias})
		default:
			return nil, fmt.Errorf("context.attachments[%d] must be string or object", i)
		}
	}
	return out, nil
}

func chooseAlias(requestedAlias string, originalName string, fallback string, used map[string]struct{}) string {
	base := normalizeAlias(requestedAlias)
	if base == "" {
		base = normalizeAlias(originalName)
	}
	if base == "" {
		base = normalizeAlias(fallback)
	}
	if base == "" {
		base = "attachment"
	}
	if _, ok := used[base]; !ok {
		return base
	}
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if _, ok := used[candidate]; !ok {
			return candidate
		}
	}
	return fmt.Sprintf("%s-%d", base, time.Now().Unix())
}

func normalizeAlias(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		return ""
	}
	if len(out) > 128 {
		out = out[:128]
	}
	return out
}

func rewritePromptMentions(prompt string, aliasToPath map[string]string) (string, map[string]string) {
	out := prompt
	replacements := map[string]string{}
	out = mentionAliasPattern.ReplaceAllStringFunc(out, func(full string) string {
		alias := strings.ToLower(strings.TrimPrefix(full, "@"))
		path, ok := aliasToPath[alias]
		if !ok {
			return full
		}
		replacements[alias] = path
		return path
	})
	return out, replacements
}

func appendAttachmentHint(prompt string, aliasToPath map[string]string) string {
	if len(aliasToPath) == 0 {
		return prompt
	}
	keys := make([]string, 0, len(aliasToPath))
	for k := range aliasToPath {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys)+2)
	lines = append(lines, "[bridge attachments]")
	for _, alias := range keys {
		lines = append(lines, fmt.Sprintf("- @%s => %s", alias, aliasToPath[alias]))
	}
	lines = append(lines, "[/bridge attachments]")
	if strings.TrimSpace(prompt) == "" {
		return strings.Join(lines, "\n")
	}
	return prompt + "\n\n" + strings.Join(lines, "\n")
}

func anyString(v any) string {
	s, _ := v.(string)
	return s
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
